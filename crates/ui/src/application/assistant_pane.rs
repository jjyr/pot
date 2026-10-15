//! Persistent conversation navigation, shared by App and Web.
use super::*;
use gpui_kit::component::{Disableable, input::Input};
use serde_json::json;
impl Shell {
    pub(super) fn assistant_compact(&self, cx: &App) -> bool {
        cx.global::<design::Viewport>().0 < px(720.)
    }
    pub(super) fn assistant_pane_width(&self, cx: &App) -> f32 {
        if self.page == 0 && !self.assistant_compact(cx) && self.assistant_pane_open {
            220.
        } else {
            0.
        }
    }
    pub(super) fn assistant_pane(&mut self, cx: &mut Context<Self>) -> Div {
        let view = self.client.view.lock().unwrap().clone();
        let search = self.assistant_search.read(cx).value().to_lowercase();
        let mut list = div()
            .id("assistant-pane-list")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_1();
        let spaces: Vec<_> = self.space_directory.spaces.values().cloned().collect();
        let names: Vec<_> = spaces
            .iter()
            .flat_map(|s| s.assistants["assistants"].as_array().into_iter().flatten())
            .filter_map(|a| a["name"].as_str())
            .map(str::to_owned)
            .collect();
        for space in spaces {
            for a in space.assistants["assistants"]
                .as_array()
                .into_iter()
                .flatten()
            {
                let Some(id) = a["id"].as_str() else {
                    continue;
                };
                let name = a["name"].as_str().unwrap_or("Pot").to_owned();
                if !search.is_empty() && !name.to_lowercase().contains(&search) {
                    continue;
                }
                let owner = space.owner(id);
                let key = owner.key();
                let reply = a["reply_id"].as_str().unwrap_or("").to_owned();
                let selected = view.client_mode == owner.mode
                    && view.assistant_id() == owner.assistant
                    && !self.assistant_list;
                let count = a["reply_count"].as_u64().unwrap_or(0);
                let seen = self
                    .seen_replies
                    .entry(key.clone())
                    .or_insert_with(|| (reply.clone(), count));
                if selected && self.page == 0 {
                    *seen = (reply.clone(), count);
                }
                let unread = if seen.0 != reply && !reply.is_empty() {
                    count.saturating_sub(seen.1).max(1)
                } else {
                    0
                };
                let approval = space
                    .permissions
                    .get(id)
                    .is_some_and(|p| p["requests"].as_array().is_some_and(|r| !r.is_empty()));
                let preview = if !space.connected {
                    i18n::text("machines.offline").to_owned()
                } else if approval {
                    i18n::text("assistant.waiting-approval").to_owned()
                } else if a["needs_configuration"] == true {
                    i18n::text("assistant.configure-required").to_owned()
                } else if a["failed"] == true {
                    i18n::text("assistant.reply-failed").to_owned()
                } else if a["responding"] == true {
                    i18n::text("client.generating-a-reply").to_owned()
                } else if a["working"].as_u64().unwrap_or(0) > 0 {
                    i18n::text("client.working").to_owned()
                } else {
                    a["preview"]
                        .as_str()
                        .filter(|s| !s.is_empty())
                        .unwrap_or(i18n::text("assistant.first-conversation"))
                        .to_owned()
                };
                let label = if names.iter().filter(|n| **n == name).count() > 1 {
                    format!("{name} · {}", space.name)
                } else {
                    name
                };
                let avatar_key = a["request_id"].as_str().unwrap_or(id);
                list = list.child(
                    Button::new(SharedString::from(format!("assistant-pane-{key}")))
                        .ghost()
                        .w_full()
                        .h(px(66.))
                        .px_2()
                        .toggled(selected)
                        .when(selected, |button| {
                            button.bg(design::assistant_selection(cx))
                        })
                        .disabled(view.assistant_pending)
                        .accessibility_label(format!("{label} · {} · {preview}", space.label()))
                        .child(
                            div()
                                .w_full()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(self.avatar_animations.render(&key, avatar_key, 32., cx))
                                .child(
                                    div()
                                        .flex_1()
                                        .min_w_0()
                                        .flex()
                                        .flex_col()
                                        .items_start()
                                        .gap_1()
                                        .child(
                                            div()
                                                .w_full()
                                                .flex()
                                                .items_center()
                                                .gap_1()
                                                .child(
                                                    div()
                                                        .flex_1()
                                                        .min_w_0()
                                                        .truncate()
                                                        .child(label),
                                                )
                                                .child(spaces_ui::badge(&space, &key))
                                                .when(unread > 0, |el| {
                                                    el.child(
                                                        div()
                                                            .min_w(px(18.))
                                                            .h(px(18.))
                                                            .px_1()
                                                            .flex()
                                                            .items_center()
                                                            .justify_center()
                                                            .rounded_full()
                                                            .text_size(px(11.))
                                                            .text_color(
                                                                cx.theme().primary_foreground,
                                                            )
                                                            .bg(cx.theme().primary)
                                                            .child(if unread > 99 {
                                                                "99+".into()
                                                            } else {
                                                                unread.to_string()
                                                            }),
                                                    )
                                                }),
                                        )
                                        .child(
                                            div()
                                                .w_full()
                                                .truncate()
                                                .text_size(px(12.))
                                                .text_color(cx.theme().muted_foreground)
                                                .child(preview),
                                        ),
                                ),
                        )
                        .on_click(cx.listener(move |this, _, window, cx| {
                            let previous = this.client.view.lock().unwrap().clone();
                            let old = format!(
                                "{}/{}",
                                previous.runtime["runtime_id"].as_str().unwrap_or(""),
                                previous.assistant_id()
                            );
                            this.chat_positions.insert(old, this.scroll.offset());
                            if this.activate_owner(&owner, window, cx) {
                                this.assistant_list = false;
                                this.assistants_ui.open = false;
                                this.assistant_drawer = false;
                                this.show_tasks = false;
                                if let Some(offset) = this.chat_positions.get(&owner.key()) {
                                    this.scroll.set_offset(*offset);
                                    this.first = false;
                                } else {
                                    this.scroll.scroll_to_bottom();
                                    this.first = true;
                                }
                                this.last_content.clear();
                                ui::command(json!({"action":"page","value":0}));
                            }
                            cx.notify();
                        })),
                );
            }
        }
        if names.is_empty() {
            list = list.child(
                div()
                    .p_3()
                    .child(ui::hint(i18n::text("assistant.empty"), cx)),
            );
        }
        div()
            .absolute()
            .left(px(56.))
            .top(px(44.))
            .bottom_0()
            .w(px(220.))
            .bg(design::assistant_pane(cx))
            .border_r_1()
            .border_color(cx.theme().border)
            .p_2()
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .px_2()
                    .py_2()
                    .child(i18n::text("spaces.assistants"))
                    .child(
                        Button::new("assistant-pane-new")
                            .ghost()
                            .small()
                            .icon(IconName::Plus)
                            .tooltip(i18n::text("assistant.new"))
                            .disabled(view.assistant_pending)
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.assistant_list = true;
                                this.assistants_ui.select("new");
                                this.assistant_drawer = false;
                                cx.notify();
                            })),
                    ),
            )
            .child(Input::new(&self.assistant_search).small())
            .child(list)
    }
}
