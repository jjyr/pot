//! Window-local route state. Back follows page hierarchy, independently of visit
//! order; restoring presentation state has no execution or configuration effects.
use crate::application::{Shell, client, gpui, ui};
use crate::pages::Control;
use gpui::{prelude::*, *};
use gpui_kit::component::menu::DropdownMenu;
use gpui_kit::component::{
    IconName, TitleBar,
    button::{Button, ButtonVariants},
};
use pot_protocol::TaskQuery;
use serde_json::json;

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Route {
    Assistant(String, Box<Route>),
    Assistants,
    Chat,
    Tasks(bool),
    Task(String),
    Schedule(String),
    Machines(serde_json::Value),
    Settings,
    Usage,
    ConversationUsage(String),
    Quota,
}
#[derive(Clone)]
pub struct State {
    assistant_id: String,
    assistant_list: bool,
    assistant_overview: bool,
    assistant_offset: Point<Pixels>,
    assistant_pane_open: bool,
    expanded: std::collections::HashSet<String>,
    configuration_assistant: Option<String>,
    usage_location: String,
    settings_return: Option<(super::spaces::Owner, String)>,
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
    offsets: [Point<Pixels>; 6],
    task_anchor: Option<(String, f32)>,
    machine_selection: serde_json::Value,
}
impl Route {
    fn scoped(self, identity: &str) -> Self {
        // The directory spans every owner; its return context belongs to the
        // window, while conversation and task contexts belong to their owner.
        if self == Self::Assistants {
            self
        } else {
            Self::Assistant(identity.into(), Box::new(self))
        }
    }
    pub fn browser_page(&self) -> &'static str {
        match self {
            Self::Assistants => "assistants",
            Self::Chat => "chat",
            Self::Task(_) => "task",
            Self::Tasks(true) | Self::Schedule(_) => "schedules",
            Self::Tasks(false) => "tasks",
            Self::Settings => "settings",
            Self::Machines(_) => "machines",
            Self::Usage => "usage",
            Self::ConversationUsage(_) => "conversation-usage",
            Self::Quota => "quota",
            Self::Assistant(_, route) => route.browser_page(),
        }
    }

    pub fn parent(&self) -> Option<Self> {
        match self {
            Self::Assistant(id, route) => route.parent().map(|parent| parent.scoped(id)),
            Self::Chat => Some(Self::Assistants),
            Self::Task(_) => Some(Self::Tasks(false)),
            Self::Schedule(_) => Some(Self::Tasks(true)),
            Self::ConversationUsage(id) if id.is_empty() => Some(Self::Chat),
            Self::ConversationUsage(id) => Some(Self::Task(id.clone())),
            Self::Quota => Some(Self::Usage),
            _ => None,
        }
    }
    fn back_label(&self) -> &'static str {
        crate::application::i18n::text(match self {
            Self::Task(_) => "navigation.back-tasks",
            Self::Schedule(_) => "navigation.back-schedules",
            Self::ConversationUsage(_) => "navigation.back-conversation",
            Self::Quota => "navigation.back-usage",
            _ => "navigation.back",
        })
    }
}
/// A cache, not a visit stack: unrelated visits never change a page's parent.
pub struct Navigation<S> {
    entries: Vec<(Route, S)>,
    current: Option<Route>,
    pub generation: u64,
}
impl<S: Clone> Default for Navigation<S> {
    fn default() -> Self {
        Self {
            entries: Vec::new(),
            current: None,
            generation: 0,
        }
    }
}
impl<S: Clone> Navigation<S> {
    pub fn observe(&mut self, route: Route, state: S) {
        if self.current.as_ref() != Some(&route) {
            self.generation += 1;
            self.current = Some(route.clone());
        }
        if let Some(entry) = self.entries.iter_mut().find(|entry| entry.0 == route) {
            entry.1 = state;
        } else {
            self.entries.push((route, state));
        }
    }
    fn state(&self, route: &Route) -> Option<S> {
        self.entries
            .iter()
            .find(|entry| &entry.0 == route)
            .map(|entry| entry.1.clone())
    }
}
impl Shell {
    pub fn route(&self, view: &client::View) -> Route {
        match self.host["page"].as_u64().unwrap_or(0) {
            1 => Route::Machines(pot_ui::machine_state::selection(
                &view.machines,
                &self.machine_selection,
            )),
            2 => Route::Settings,
            4 => Route::Usage,
            5 => Route::ConversationUsage(view.selected.clone()),
            6 => Route::Quota,
            _ if self.show_tasks && self.schedule_tab && !view.schedule_selected.is_empty() => {
                Route::Schedule(view.schedule_selected.clone())
            }
            _ if self.show_tasks => Route::Tasks(self.schedule_tab),
            _ if !view.selected.is_empty() => Route::Task(view.selected.clone()),
            _ if self.assistant_overview => Route::Assistants,
            _ => Route::Chat,
        }
    }
    pub fn observe_navigation(&mut self) {
        if self.pending_offsets.is_some() {
            return;
        }
        let v = self.client.view.lock().unwrap().clone();
        let route = self.route(&v).scoped(&format!(
            "{}/{}",
            v.runtime["runtime_id"].as_str().unwrap_or_default(),
            v.assistant_id()
        ));
        self.navigation.observe(route, self.capture_navigation());
    }
    pub(super) fn capture_navigation(&self) -> State {
        let v = self.client.view.lock().unwrap().clone();
        let y = -f32::from(self.tasks_scroll.offset().y);
        let task_anchor = v
            .tasks
            .get((y / 80.).floor() as usize)
            .map(|t| (t.id.clone(), y % 80.));
        State {
            assistant_id: v.assistant_id().to_owned(),
            assistant_list: self.assistant_list,
            assistant_overview: self.assistant_overview,
            assistant_offset: self.assistant_list_scroll.offset(),
            assistant_pane_open: self.assistant_pane_open,
            expanded: self.expanded.clone(),
            configuration_assistant: self.configuration_assistant.clone(),
            usage_location: self.usage_location.clone(),
            settings_return: self.settings_return.clone(),
            page: self.host["page"].as_u64().unwrap_or(0),
            selected: v.selected,
            show_tasks: self.show_tasks,
            machine_selection: pot_ui::machine_state::selection(
                &v.machines,
                &self.machine_selection,
            ),
            schedule_tab: self.schedule_tab,
            query: v.task_query,
            schedule_selected: v.schedule_selected,
            schedule_offset: v.schedule_offset,
            runs_offset: v.runs_offset,
            days: self.usage_ui.days,
            group_offset: self.usage_ui.group_offset,
            settings_section: self.host["settings"]["section"].as_u64().unwrap_or(0),
            task_anchor,
            offsets: [
                self.scroll.offset(),
                self.tasks_scroll.offset(),
                self.settings.scroll.offset(),
                self.schedules_ui.scroll.offset(),
                self.usage_ui.scroll.offset(),
                self.machines_scroll.offset(),
            ],
        }
    }
    pub fn navigate_up(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if !self.editors.is_empty() {
            self.editor_intents.push(json!({"action":"editor-back"}));
            cx.notify();
            return;
        }
        self.observe_navigation();
        let view = self.client.view.lock().unwrap().clone();
        let route = self.route(&view);
        if matches!(route, Route::Chat) {
            self.chat_positions.insert(
                format!(
                    "{}/{}",
                    view.runtime["runtime_id"].as_str().unwrap_or_default(),
                    view.assistant_id()
                ),
                self.scroll.offset(),
            );
        }
        let Some(parent) = route.parent() else {
            return;
        };
        let identity = format!(
            "{}/{}",
            view.runtime["runtime_id"].as_str().unwrap_or_default(),
            view.assistant_id()
        );
        let scoped = |route: Route| route.scoped(&identity);
        let Some(mut state) = self
            .navigation
            .state(&scoped(parent.clone()))
            .or_else(|| self.navigation.state(&scoped(route)))
        else {
            return;
        };
        // Direct links can open a child before its parent was ever visited.
        match parent {
            Route::Assistants => {
                state.page = 0;
                state.show_tasks = false;
                state.assistant_overview = true;
                state.assistant_list = false;
                state.selected.clear();
            }
            Route::Tasks(scheduled) => {
                state.page = 3;
                state.show_tasks = true;
                state.schedule_tab = scheduled;
                state.selected.clear();
                state.schedule_selected.clear();
            }
            Route::Chat | Route::Task(_) => {
                state.page = 0;
                state.show_tasks = false;
                state.selected = match parent {
                    Route::Task(id) => id,
                    _ => String::new(),
                };
            }
            Route::Usage => {
                state.page = 4;
            }
            _ => unreachable!("parent routes are defined by Route::parent"),
        }
        self.restore_navigation(state, window, cx);
    }
    pub(super) fn restore_navigation(
        &mut self,
        state: State,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.assistant_list = state.assistant_list;
        self.assistant_overview = state.assistant_overview;
        self.assistant_list_scroll
            .set_offset(state.assistant_offset);
        self.assistant_pane_open = state.assistant_pane_open;
        self.expanded = state.expanded;
        self.configuration_assistant = state.configuration_assistant;
        self.usage_location = state.usage_location;
        self.settings_return = state.settings_return;
        self.client.select_assistant(state.assistant_id);
        self.client.select(state.selected);
        self.show_tasks = state.show_tasks;
        self.machine_selection = pot_ui::machine_state::selection(
            &self.client.view.lock().unwrap().machines,
            &state.machine_selection,
        );
        ui::command(serde_json::json!({"action":"machines-detail","value":true}));
        self.schedule_tab = state.schedule_tab;
        self.task_search
            .update(cx, |f, cx| f.set_value(state.query.q.clone(), window, cx));
        self.client.view.lock().unwrap().task_query.execution_target =
            state.query.execution_target.clone();
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
            self.host["timezone"]
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
        self.machines_scroll.set_offset(state.offsets[5]);
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
        self.host["page"] = json!(state.page);
        if state.page == 2 {
            self.settings_state.state["section"] = json!(state.settings_section);
        }
        self.project_page(state.page);
        cx.notify();
    }
    pub fn titlebar(
        &mut self,
        view: &client::View,
        window: &Window,
        cx: &mut Context<Self>,
    ) -> TitleBar {
        let route = self.route(view);
        let title: String = match &route {
            Route::Assistant(_, _) => unreachable!("cache identity only"),
            Route::Assistants => crate::application::i18n::text("spaces.assistants").into(),
            Route::Chat => {
                if self.assistant_list {
                    crate::application::i18n::text("spaces.assistants").into()
                } else {
                    view.assistants["assistants"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .find(|a| a["id"] == view.assistant_id())
                        .and_then(|a| a["name"].as_str())
                        .unwrap_or("Pot")
                        .to_owned()
                }
            }
            Route::Tasks(scheduled) => crate::application::i18n::text(if *scheduled {
                "navigation.schedules"
            } else {
                "navigation.tasks"
            })
            .into(),
            Route::Task(_) => view
                .selected_task
                .as_ref()
                .map(|task| task.title.clone())
                .unwrap_or(crate::application::i18n::text("navigation.task-details").into()),
            Route::Schedule(_) => view
                .schedules
                .selected
                .as_ref()
                .map(|schedule| schedule.spec.title.clone())
                .unwrap_or(crate::application::i18n::text("navigation.schedule-details").into()),
            Route::Settings => crate::application::i18n::text("ui.settings").into(),
            Route::Machines(_) => crate::application::i18n::text("machines.title").into(),
            Route::Usage => crate::application::i18n::text("navigation.usage").into(),
            Route::Quota => crate::application::i18n::text("navigation.usage-limits").into(),
            Route::ConversationUsage(_) => {
                crate::application::i18n::text("navigation.conversation-usage").into()
            }
        };
        let editing = !self.editors.is_empty();
        let has_parent = route.parent().is_some() && (!matches!(route, Route::Chat) || self.narrow);
        let title = self.editor_title().unwrap_or(title);
        let actions_width = match route {
            Route::Chat => {
                if pot_ui::design::compact(cx) {
                    104.
                } else {
                    180.
                }
            }
            Route::Task(_) => 152.,
            _ => 0.,
        };
        let chat_back = editing;
        let back_label = if editing {
            crate::application::i18n::text("navigation.back")
        } else {
            route.back_label()
        };
        let back_width = if has_parent || chat_back { 36. } else { 0. };
        let mobile = pot_ui::design::mobile(cx);
        let mobile_insets = if super::platform::with(|p| p.native_window()) {
            112.
        } else {
            28.
        } + if !editing && !has_parent { 52. } else { 0. }
            + if has_parent || editing { 52. } else { 0. }
            + if !editing && matches!(route, Route::Chat) && !self.assistant_list {
                84.
            } else {
                0.
            };
        let title_width = (window.fully_visible_bounds().size.width
            - px(if mobile {
                mobile_insets
            } else {
                112. + back_width + actions_width
            }))
        .max(px(40.));
        let visible_title = pot_ui::pages::single_line_title(&title, 14., title_width, window, cx);
        TitleBar::new()
            .h(px(ui::TITLEBAR))
            .pl(px(if mobile {
                if super::platform::with(|p| p.native_window()) {
                    96.
                } else {
                    12.
                }
            } else if super::platform::with(|p| p.native_window()) {
                96. + self.assistant_pane_width(cx)
            } else {
                72. + self.assistant_pane_width(cx)
            }))
            .pr_4()
            .bg(pot_ui::design::navigation(cx))
            .border_b_0()
            .child(
                div()
                    .flex()
                    .items_center()
                    .gap_2()
                    .flex_1()
                    .min_w_0()
                    .when(mobile && !editing && !has_parent, |el| {
                        el.child(
                            Button::new("open-navigation")
                                .ghost()
                                .control(cx)
                                .icon(IconName::Menu)
                                .accessibility_label(crate::application::i18n::text(
                                    "navigation.open-menu",
                                ))
                                .on_click(cx.listener(|this, _, window, cx| {
                                    this.navigation_return_focus = window.focused(cx);
                                    this.set_navigation_open(true, cx);
                                    this.navigation_focus.focus(window, cx);
                                    cx.notify();
                                })),
                        )
                    })
                    .when(has_parent || chat_back, |el| {
                        el.child(
                            Button::new("navigate-up")
                                .ghost()
                                .control(cx)
                                .icon(IconName::ChevronLeft)
                                .accessibility_label(back_label)
                                .tooltip(back_label)
                                .on_click(
                                    cx.listener(|this, _, window, cx| this.navigate_up(window, cx)),
                                ),
                        )
                    })
                    .when(!editing && matches!(route, Route::Chat) && !mobile, |el| {
                        el.child(
                            Button::new("toggle-assistant-pane")
                                .ghost()
                                .control(cx)
                                .accessibility_label(crate::application::i18n::text(
                                    "assistant.toggle-list",
                                ))
                                .icon(if self.assistant_pane_width(cx) > 0. {
                                    IconName::ChevronLeft
                                } else {
                                    IconName::ChevronRight
                                })
                                .tooltip(crate::application::i18n::text("assistant.toggle-list"))
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.assistant_pane_open = !this.assistant_pane_open;
                                    cx.notify();
                                })),
                        )
                    })
                    .child(
                        div()
                            .flex_1()
                            .min_w_0()
                            .flex()
                            .items_center()
                            .gap_2()
                            .when(matches!(route, Route::Chat) && !self.assistant_list, |el| {
                                let key = view.assistants["assistants"]
                                    .as_array()
                                    .into_iter()
                                    .flatten()
                                    .find(|a| a["id"] == view.assistant_id())
                                    .and_then(|a| a["request_id"].as_str())
                                    .unwrap_or(view.assistant_id());
                                let owner = self
                                    .space_directory
                                    .spaces
                                    .values()
                                    .find(|s| s.mode == view.client_mode)
                                    .map(|s| s.owner(view.assistant_id()).key())
                                    .unwrap_or_default();
                                el.child(self.avatar_animations.render(&owner, key, 24., cx))
                            })
                            .child(
                                div()
                                    .id("route-title")
                                    .min_w_0()
                                    .truncate()
                                    .text_size(px(14.))
                                    .font_weight(FontWeight::MEDIUM)
                                    .max_w(title_width)
                                    .child(visible_title)
                                    .tooltip(move |window, cx| {
                                        ui::title_tooltip(title.clone(), window, cx)
                                    }),
                            ),
                    )
                    .when(
                        !mobile
                            && !editing
                            && matches!(self.route(view), Route::Chat)
                            && !self.assistant_list,
                        |el| {
                            let owner = view.assistant_id().to_owned();
                            el.when(!mobile, |el| {
                                el.when_some(
                                    self.space_directory.spaces.get(&view.client_mode),
                                    |el, s| {
                                        el.child(crate::application::spaces_ui::badge(
                                            s, "titlebar",
                                        ))
                                    },
                                )
                            })
                            .child(
                                Button::new("edit-chat-assistant")
                                    .ghost()
                                    .control(cx)
                                    .icon(IconName::Settings)
                                    .tooltip(crate::application::i18n::text(
                                        "spaces.edit-assistant",
                                    ))
                                    .accessibility_label(crate::application::i18n::text(
                                        "spaces.edit-assistant",
                                    ))
                                    .on_click(cx.listener(move |this, _, window, cx| {
                                        this.open_assistant_editor(&owner, window, cx);
                                    })),
                            )
                        },
                    )
                    .when(
                        mobile && !editing && matches!(route, Route::Chat) && !self.assistant_list,
                        |el| {
                            let entity = cx.entity();
                            let owner = view.assistant_id().to_owned();
                            el.child(
                                Button::new("conversation-actions")
                                    .ghost()
                                    .control(cx)
                                    .icon(IconName::Ellipsis)
                                    .accessibility_label(crate::application::i18n::text(
                                        "navigation.conversation-actions",
                                    ))
                                    .dropdown_menu(move |menu, _, cx| {
                                        let edit_entity = entity.clone();
                                        let owner = owner.clone();
                                        let usage_entity = entity.clone();
                                        menu.item(
                                            crate::pages::menu_item(
                                                crate::application::i18n::text(
                                                    "spaces.edit-assistant",
                                                ),
                                                cx,
                                            )
                                            .on_click(
                                                move |_, window, cx| {
                                                    edit_entity.update(cx, |this, cx| {
                                                        this.open_assistant_editor(
                                                            &owner, window, cx,
                                                        );
                                                    });
                                                },
                                            ),
                                        )
                                        .item(
                                            crate::pages::menu_item(
                                                crate::application::i18n::text(
                                                    "navigation.conversation-usage",
                                                ),
                                                cx,
                                            )
                                            .on_click(
                                                move |_, _, cx| {
                                                    usage_entity.update(cx, |this, cx| {
                                                        this.usage_ui.open_settings();
                                                        ui::command(
                                                            json!({"action":"page","value":5}),
                                                        );
                                                        cx.notify();
                                                    });
                                                },
                                            ),
                                        )
                                    }),
                            )
                        },
                    )
                    .when(
                        !editing
                            && matches!(self.route(view), Route::Chat)
                            && !self.assistant_list
                            && !pot_ui::design::compact(cx),
                        |el| {
                            el.child(div().max_w(px(240.)).truncate().child(ui::hint(
                                format!(
                            "{} · {}",
                            self.host["settings"]["active_name"]
                                .as_str()
                                .unwrap_or("Agent"),
                            self.host["settings"]["active_spec"]["model"]
                                .as_str()
                                .unwrap_or("")
                        ),
                                cx,
                            )))
                        },
                    )
                    .when(
                        !editing
                            && !self.show_tasks
                            && (!mobile || !matches!(route, Route::Chat))
                            && matches!(self.route(view), Route::Chat | Route::Task(_))
                            && !self.assistant_list,
                        |el| el.child(crate::application::usage_ui::entry(view, cx)),
                    ),
            )
    }
}

#[cfg(test)]
mod tests {
    use super::{Navigation, Route};
    fn scoped(assistant: &str, route: Route) -> Route {
        route.scoped(assistant)
    }
    #[test]
    fn back_follows_hierarchy_even_when_opened_from_chat() {
        let mut states = Navigation::default();
        states.observe(Route::Tasks(false), 1);
        states.observe(Route::Tasks(false), 2);
        states.observe(Route::Chat, 3);
        states.observe(Route::Task("a".into()), 4);
        let parent = Route::Task("a".into()).parent().unwrap();
        assert_eq!(parent, Route::Tasks(false));
        assert_eq!(states.state(&parent), Some(2));
        assert_eq!(
            Route::ConversationUsage("a".into()).parent(),
            Some(Route::Task("a".into()))
        );
        assert_eq!(
            Route::ConversationUsage(String::new()).parent(),
            Some(Route::Chat)
        );
        assert_eq!(
            Route::Schedule("s".into()).parent(),
            Some(Route::Tasks(true))
        );
        assert_eq!(Route::Quota.parent(), Some(Route::Usage));
    }
    #[test]
    fn browser_routes_preserve_the_same_detail_and_overview_semantics() {
        assert_eq!(Route::Assistants.browser_page(), "assistants");
        assert_eq!(Route::Chat.browser_page(), "chat");
        assert_eq!(Route::Chat.parent(), Some(Route::Assistants));
        assert_eq!(Route::Task("task".into()).browser_page(), "task");
        assert_eq!(Route::Tasks(false).browser_page(), "tasks");
        assert_eq!(Route::Tasks(true).browser_page(), "schedules");
        assert_eq!(Route::Schedule("plan".into()).browser_page(), "schedules");
        assert_eq!(
            Route::ConversationUsage("task".into()).browser_page(),
            "conversation-usage"
        );
    }
    #[test]
    fn root_pages_have_no_back() {
        for route in [
            Route::Assistants,
            Route::Tasks(false),
            Route::Tasks(true),
            Route::Settings,
            Route::Usage,
            Route::Machines(serde_json::json!({"runtime_id":"a"})),
        ] {
            assert_eq!(route.parent(), None);
        }
        assert_eq!(
            Route::Task("direct".into()).parent(),
            Some(Route::Tasks(false))
        );
    }
    #[test]
    fn assistant_directory_return_context_is_shared_across_owners() {
        let mut states = Navigation::default();
        states.observe(scoped("runtime/a", Route::Assistants), 1);
        states.observe(scoped("runtime/b", Route::Chat), 2);
        states.observe(scoped("runtime/b", Route::Assistants), 3);
        states.observe(scoped("runtime/a", Route::Chat), 4);
        let parent = scoped("runtime/a", Route::Chat).parent().unwrap();
        assert_eq!(states.state(&parent), Some(3));
        assert_eq!(states.state(&scoped("runtime/b", Route::Chat)), Some(2));
    }
    #[test]
    fn route_cache_is_scoped_and_retains_parent_state() {
        let mut states = Navigation::default();
        states.observe(scoped("runtime-a/assistant-a", Route::Tasks(false)), 1);
        states.observe(scoped("runtime-a/assistant-b", Route::Tasks(false)), 2);
        states.observe(scoped("runtime-b/assistant-a", Route::Tasks(false)), 3);
        states.observe(scoped("runtime-a/assistant-a", Route::Tasks(true)), 4);
        states.observe(scoped("runtime-a/assistant-a", Route::Task("t".into())), 5);
        let parent = scoped("runtime-a/assistant-a", Route::Task("t".into()))
            .parent()
            .unwrap();
        assert_eq!(states.state(&parent), Some(1));
        states.observe(parent.clone(), 6);
        let generation = states.generation;
        states.observe(parent.clone(), 7);
        assert_eq!(states.generation, generation);
        assert_eq!(states.state(&parent), Some(7));
        assert_eq!(
            states.state(&scoped("runtime-a/assistant-a", Route::Tasks(true))),
            Some(4)
        );
    }
}
