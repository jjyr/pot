//! Window-local route history. Filters and presentation state belong to entries,
//! never to route identity; execution and configuration are not navigation effects.
use crate::{Shell, client, gpui, ui};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable,
    button::{Button, ButtonVariants},
};
use pot_protocol::TaskQuery;
use serde_json::json;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Route {
    Assistant(String, Box<Route>),
    Chat,
    Tasks,
    Task(String),
    Schedule(String),
    Sandbox,
    Settings,
    Usage,
    ConversationUsage(String),
    Quota,
}
#[derive(Clone)]
pub struct State {
    assistant_id: String,
    page: u64,
    selected: String,
    show_tasks: bool,
    schedule_tab: bool,
    query: TaskQuery,
    schedule_selected: String,
    schedule_offset: usize,
    runs_offset: usize,
    days: u32,
    group_offset: usize,
    settings_section: u64,
    offsets: [Point<Pixels>; 5],
    task_anchor: Option<(String, f32)>,
}
pub struct History<S> {
    entries: Vec<(Route, S)>,
    cursor: usize,
    pub generation: u64,
}
impl<S: Clone> Default for History<S> {
    fn default() -> Self {
        Self {
            entries: Vec::new(),
            cursor: 0,
            generation: 0,
        }
    }
}
impl<S: Clone> History<S> {
    pub fn observe(&mut self, route: Route, state: S) {
        if self.entries.get(self.cursor).is_some_and(|e| e.0 == route) {
            self.entries[self.cursor].1 = state;
            return;
        }
        self.entries.truncate(self.cursor + 1);
        self.entries.push((route, state));
        self.cursor = self.entries.len() - 1;
        self.generation += 1;
    }
    pub fn can_back(&self) -> bool {
        self.cursor > 0
    }
    pub fn can_forward(&self) -> bool {
        self.cursor + 1 < self.entries.len()
    }
    fn travel(&mut self, forward: bool) -> Option<S> {
        if forward && self.can_forward() {
            self.cursor += 1;
        } else if !forward && self.can_back() {
            self.cursor -= 1;
        } else {
            return None;
        }
        self.generation += 1;
        Some(self.entries[self.cursor].1.clone())
    }
}
impl Shell {
    pub fn route(&self, view: &client::View) -> Route {
        match self.native["page"].as_u64().unwrap_or(0) {
            1 => Route::Sandbox,
            2 => Route::Settings,
            4 => Route::Usage,
            5 => Route::ConversationUsage(view.selected.clone()),
            6 => Route::Quota,
            _ if self.show_tasks && self.schedule_tab && !view.schedule_selected.is_empty() => {
                Route::Schedule(view.schedule_selected.clone())
            }
            _ if self.show_tasks => Route::Tasks,
            _ if !view.selected.is_empty() => Route::Task(view.selected.clone()),
            _ => Route::Chat,
        }
    }
    pub fn observe_navigation(&mut self) {
        if self.pending_offsets.is_some() {
            return;
        }
        let v = self.client.view.lock().unwrap().clone();
        let route = Route::Assistant(
            format!(
                "{}/{}",
                v.runtime["runtime_id"].as_str().unwrap_or_default(),
                v.assistant_id()
            ),
            Box::new(self.route(&v)),
        );
        let y = -f32::from(self.tasks_scroll.offset().y);
        let task_anchor = v
            .tasks
            .get((y / 80.).floor() as usize)
            .map(|t| (t.id.clone(), y % 80.));
        self.history.observe(
            route,
            State {
                assistant_id: v.assistant_id().to_owned(),
                page: self.native["page"].as_u64().unwrap_or(0),
                selected: v.selected,
                show_tasks: self.show_tasks,
                schedule_tab: self.schedule_tab,
                query: v.task_query,
                schedule_selected: v.schedule_selected,
                schedule_offset: v.schedule_offset,
                runs_offset: v.runs_offset,
                days: self.usage_ui.days,
                group_offset: self.usage_ui.group_offset,
                settings_section: self.native["settings"]["section"].as_u64().unwrap_or(0),
                task_anchor,
                offsets: [
                    self.scroll.offset(),
                    self.tasks_scroll.offset(),
                    self.settings.scroll.offset(),
                    self.schedules_ui.scroll.offset(),
                    self.usage_ui.scroll.offset(),
                ],
            },
        );
    }
    pub fn travel(&mut self, forward: bool, window: &mut Window, cx: &mut Context<Self>) {
        self.observe_navigation();
        let Some(state) = self.history.travel(forward) else {
            return;
        };
        self.client.select_assistant(state.assistant_id);
        self.client.select(state.selected);
        self.show_tasks = state.show_tasks;
        self.schedule_tab = state.schedule_tab;
        self.task_search
            .update(cx, |f, cx| f.set_value(state.query.q.clone(), window, cx));
        self.client
            .filter_tasks(state.query.scope, state.query.q, state.query.offset);
        {
            let mut v = self.client.view.lock().unwrap();
            v.schedule_selected = state.schedule_selected;
            v.schedule_offset = state.schedule_offset;
            v.runs_offset = state.runs_offset;
        }
        self.usage_ui.days = state.days;
        self.usage_ui.group_offset = state.group_offset;
        self.usage_ui.group_context = Some((
            if state.page == 4 {
                String::new()
            } else {
                self.client.view.lock().unwrap().selected.clone()
            },
            state.days,
            self.native["timezone"]
                .as_str()
                .unwrap_or("Asia/Shanghai")
                .into(),
        ));
        self.pending_offsets = Some(state.offsets);
        self.pending_task_anchor = state.task_anchor;
        self.scroll.set_offset(state.offsets[0]);
        self.tasks_scroll.set_offset(state.offsets[1]);
        self.settings.scroll.set_offset(state.offsets[2]);
        self.schedules_ui.scroll.set_offset(state.offsets[3]);
        self.usage_ui.scroll.set_offset(state.offsets[4]);
        // The next snapshot refresh must not scroll a restored conversation.
        self.last_content = self
            .client
            .view
            .lock()
            .unwrap()
            .snapshot
            .as_ref()
            .map(|s| {
                s.messages
                    .iter()
                    .map(|m| format!("{}:{}:{:?}", m.id, m.text.len(), m.status))
                    .collect::<Vec<_>>()
                    .join(";")
            })
            .unwrap_or_default();
        self.first = false;
        self.native["page"] = json!(state.page);
        if state.page == 2 {
            ui::command(json!({"action":"history-section","value":state.settings_section}));
        }
        ui::command(json!({"action":"page","value":state.page}));
        cx.notify();
    }
    pub fn toolbar(&self, view: &client::View, cx: &mut Context<Self>) -> Div {
        let route = self.route(view);
        let (context, title) = match route {
            Route::Assistant(_, _) => unreachable!("history identity only"),
            Route::Chat => ("", crate::i18n::text("navigation.chat").into()),
            Route::Tasks => (
                "",
                if self.schedule_tab {
                    crate::i18n::text("navigation.schedules")
                } else {
                    crate::i18n::text("navigation.tasks")
                }
                .into(),
            ),
            Route::Task(_) => (
                crate::i18n::text("navigation.tasks"),
                view.selected_task
                    .as_ref()
                    .map(|t| t.title.clone())
                    .unwrap_or(crate::i18n::text("navigation.task-details").into()),
            ),
            Route::Schedule(_) => (
                crate::i18n::text("navigation.schedules"),
                view.schedules
                    .selected
                    .as_ref()
                    .map(|p| p.spec.title.clone())
                    .unwrap_or(crate::i18n::text("navigation.schedule-details").into()),
            ),
            Route::Settings => ("", crate::i18n::text("ui.settings").into()),
            Route::Sandbox => ("", "Sandbox".into()),
            Route::Usage => ("", crate::i18n::text("navigation.usage").into()),
            Route::Quota => (
                crate::i18n::text("navigation.usage"),
                crate::i18n::text("navigation.usage-limits").into(),
            ),
            Route::ConversationUsage(_) => (
                crate::i18n::text("navigation.usage"),
                if view.selected.is_empty() {
                    crate::i18n::text("navigation.main-chat")
                } else {
                    crate::i18n::text("navigation.task-conversation")
                }
                .into(),
            ),
        };
        div()
            .h(px(56.))
            .flex_shrink_0()
            .pl(px(ui::SIDEBAR + 24.))
            .pr_6()
            .bg(crate::design::navigation(cx))
            .border_b_1()
            .border_color(cx.theme().border)
            .flex()
            .items_center()
            .gap_3()
            .child(
                div()
                    .flex()
                    .gap_1()
                    .child(
                        Button::new("history-back")
                            .ghost()
                            .small()
                            .icon(IconName::ChevronLeft)
                            .accessibility_label(crate::i18n::text("navigation.back"))
                            .tooltip(crate::i18n::text("navigation.back-11"))
                            .disabled(!self.history.can_back())
                            .on_click(
                                cx.listener(|this, _, window, cx| this.travel(false, window, cx)),
                            ),
                    )
                    .child(
                        Button::new("history-forward")
                            .ghost()
                            .small()
                            .icon(IconName::ChevronRight)
                            .accessibility_label(crate::i18n::text("navigation.forward"))
                            .tooltip(crate::i18n::text("navigation.forward-13"))
                            .disabled(!self.history.can_forward())
                            .on_click(
                                cx.listener(|this, _, window, cx| this.travel(true, window, cx)),
                            ),
                    ),
            )
            .child(
                div()
                    .flex_1()
                    .min_w_0()
                    .flex()
                    .items_center()
                    .gap_2()
                    .when(!context.is_empty(), |el| {
                        el.child(ui::hint(context, cx)).child(ui::hint("›", cx))
                    })
                    .child(
                        div()
                            .id("route-title")
                            .min_w_0()
                            .truncate()
                            .text_size(px(14.))
                            .font_weight(FontWeight::MEDIUM)
                            .child(title.clone())
                            .tooltip(move |window, cx| {
                                ui::title_tooltip(title.clone(), window, cx)
                            }),
                    ),
            )
            .when(matches!(self.route(view), Route::Chat), |el| {
                el.child(div().max_w(px(240.)).truncate().child(ui::hint(
                    format!(
                            "{} · {}",
                            self.native["settings"]["active_name"]
                                .as_str()
                                .unwrap_or("Agent"),
                            self.native["settings"]["active_spec"]["model"]
                                .as_str()
                                .unwrap_or("")
                        ),
                    cx,
                )))
            })
            .when(
                !self.show_tasks && matches!(self.route(view), Route::Chat | Route::Task(_)),
                |el| el.child(crate::usage_ui::entry(view, cx)),
            )
    }
}

#[cfg(test)]
mod tests {
    use super::{History, Route};
    #[test]
    fn assistant_and_runtime_are_distinct_navigation_destinations() {
        let mut history = History::default();
        history.observe(
            Route::Assistant("runtime-a/assistant-a".into(), Box::new(Route::Chat)),
            1,
        );
        history.observe(
            Route::Assistant("runtime-a/assistant-b".into(), Box::new(Route::Chat)),
            2,
        );
        history.observe(
            Route::Assistant("runtime-b/assistant-a".into(), Box::new(Route::Chat)),
            3,
        );
        assert_eq!(history.travel(false), Some(2));
        assert_eq!(history.travel(false), Some(1));
        assert_eq!(history.travel(true), Some(2));
    }
    #[test]
    fn local_state_does_not_consume_history_and_new_destination_discards_forward_branch() {
        let mut h = History::default();
        h.observe(Route::Chat, 0);
        assert!(!h.can_back() && !h.can_forward());
        h.observe(Route::Tasks, 1);
        h.observe(Route::Tasks, 2); // search/filter/tab updates checkpoint local state.
        h.observe(Route::Task("a".into()), 3);
        assert_eq!(h.travel(false), Some(2));
        assert!(h.can_forward());
        assert_eq!(h.travel(true), Some(3));
        assert_eq!(h.travel(false), Some(2));
        h.observe(Route::Settings, 4);
        assert!(!h.can_forward());
        assert_eq!(h.travel(false), Some(2));
        assert_eq!(h.travel(false), Some(0));
        assert_eq!(h.travel(false), None);
    }
}
