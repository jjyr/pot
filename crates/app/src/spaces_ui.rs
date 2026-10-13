use crate::{
    Shell, backend, gpui,
    spaces::{Owner, Space},
    ui,
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable,
    button::{Button, ButtonVariants},
};
use serde_json::{Value, json};

pub fn badge(space: &Space, context: &str) -> Button {
    Button::new(SharedString::from(format!(
        "space-badge-{}-{context}",
        space.id
    )))
    .ghost()
    .small()
    .icon(if space.mode == "local" {
        gpui_kit::assets::IconName::Monitor
    } else {
        gpui_kit::assets::IconName::Cloud
    })
    .tooltip(space.label())
    .accessibility_label(space.label())
}
impl Shell {
    pub fn capture_chat(&self) -> Option<(Owner, String)> {
        let view = self.client.view.lock().unwrap();
        self.space_directory
            .spaces
            .get(&view.client_mode)
            .map(|s| (s.owner(view.assistant_id()), view.selected.clone()))
    }
    pub fn activate_owner(
        &mut self,
        owner: &Owner,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) -> bool {
        let valid = self
            .space_directory
            .spaces
            .get(&owner.mode)
            .is_some_and(|s| s.id == owner.control);
        if !valid {
            self.client.view.lock().unwrap().command_error =
                crate::i18n::text("spaces.identity-changed").into();
            return false;
        }
        let query = self.client.view.lock().unwrap().task_query.clone();
        if backend::mode() != owner.mode {
            self.switch_mode(window, cx);
        }
        self.client.select_assistant(owner.assistant.clone());
        if self.client.view.lock().unwrap().assistant_id() != owner.assistant {
            self.client.view.lock().unwrap().command_error =
                crate::i18n::text("spaces.wait-send").into();
            return false;
        }
        self.client.view.lock().unwrap().task_query.execution_target =
            query.execution_target.clone();
        self.client.select(String::new());
        self.client
            .filter_tasks(query.scope, query.q.clone(), query.offset);
        self.task_search
            .update(cx, |f, cx| f.set_value(query.q, window, cx));
        true
    }
    pub fn notification_banner(&self, cx: &mut Context<Self>) -> Div {
        let mut row = div().ml(px(56.)).flex().items_center().gap_2();
        for space in self.space_directory.spaces.values() {
            if let Some((assistant, _)) = space
                .permissions
                .iter()
                .find(|(_, book)| book["requests"].as_array().is_some_and(|r| !r.is_empty()))
            {
                let owner = space.owner(assistant);
                return row
                    .px_8()
                    .py_2()
                    .child(
                        Button::new("space-approval-notice")
                            .ghost()
                            .label(format!(
                                "{} · {}",
                                space.assistant_name(assistant),
                                crate::i18n::text("permissions.request")
                            ))
                            .on_click(cx.listener(move |this, _, window, cx| {
                                if this.activate_owner(&owner, window, cx) {
                                    this.assistant_list = false;
                                    this.show_tasks = false;
                                    ui::command(json!({"action":"page","value":0}));
                                }
                                cx.notify();
                            })),
                    )
                    .child(badge(space, "approval"));
            }
        }
        if let Some((owner, task)) = self.notifications.first() {
            let owner = owner.clone();
            let task = task.clone();
            let name = self
                .space_directory
                .spaces
                .get(&owner.mode)
                .map(|s| s.assistant_name(&owner.assistant))
                .unwrap_or("Pot");
            let label = format!(
                "{name} · {} · {}",
                task.title,
                crate::task_phase(&task.phase)
            );
            row = row.px_8().py_2().child(
                Button::new("space-notification")
                    .ghost()
                    .label(label)
                    .on_click(cx.listener(move |this, _, window, cx| {
                        if this.activate_owner(&owner, window, cx) {
                            this.client.select(task.id.clone());
                            this.show_tasks = false;
                            this.assistant_list = false;
                            this.notifications.remove(0);
                            ui::command(json!({"action":"page","value":3}));
                        }
                        cx.notify();
                    })),
            );
            if let Some(s) = self
                .space_directory
                .spaces
                .get(&self.notifications[0].0.mode)
            {
                row = row.child(badge(s, "notification"));
            }
            row = row.child(
                Button::new("dismiss-space-notification")
                    .ghost()
                    .icon(IconName::Close)
                    .tooltip(crate::i18n::text("assistant.close"))
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.notifications.remove(0);
                        cx.notify();
                    })),
            );
        }
        row
    }
    pub fn collaboration_updates(&self, view: &crate::client::View, cx: &mut Context<Self>) -> Div {
        let mut body = div().flex().flex_col().gap_3();
        if !view.selected.is_empty() {
            return body;
        }
        let Some(space) = self.space_directory.spaces.get(&view.client_mode) else {
            return body;
        };
        let Some(records) = space.mail.get(view.assistant_id()) else {
            return body;
        };
        for record in crate::spaces::chat_updates(records, &space.owner(view.assistant_id())) {
            let peer = self
                .space_directory
                .spaces
                .values()
                .find(|s| s.id == record.message.from.control_id);
            let name = peer
                .and_then(|s| s.assistants["assistants"].as_array())
                .into_iter()
                .flatten()
                .find(|a| a["id"] == record.message.from.assistant_id)
                .and_then(|a| a["name"].as_str())
                .unwrap_or(&record.message.from.assistant_id);
            body = body.child(
                ui::card(cx)
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(format!("{} · {name}", crate::i18n::text("spaces.reply")))
                            .when_some(peer, |el, s| el.child(badge(s, &record.message.id))),
                    )
                    .child(div().whitespace_normal().child(record.message.text)),
            );
        }
        body
    }
    pub fn render_assistant_list(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let mut body = ui::content().child(
            div()
                .flex()
                .justify_between()
                .child(ui::title(crate::i18n::text("spaces.assistants")))
                .child(
                    Button::new("assistant-new-top")
                        .primary()
                        .icon(IconName::Plus)
                        .label(crate::i18n::text("assistant.new"))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.assistants_ui.form_only = true;
                            this.assistants_ui.select("new");
                            cx.notify();
                        })),
                ),
        );
        if self.assistants_ui.open {
            body = body.child(self.location_bar("create", cx));
            let view = self.client.view.lock().unwrap().clone();
            self.assistants_ui.form_only = true;
            let handler = self.page_handler(cx);
            body = body.child(self.assistants_ui.render(
                pot_ui::assistants::View {
                    assistants: &view.assistants,
                    current: view.assistant_id(),
                    rules: &view.assistant_rules,
                },
                self.catalog(),
                handler,
                window,
                cx,
            ));
            if !view.command_error.is_empty() {
                body = body.child(ui::notice(view.command_error, true, cx));
            }
        }
        let spaces = self
            .space_directory
            .spaces
            .values()
            .cloned()
            .collect::<Vec<_>>();
        let names: Vec<String> = spaces
            .iter()
            .flat_map(|s| {
                s.assistants["assistants"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .filter_map(|a| a["name"].as_str().map(str::to_owned))
            })
            .collect();
        let mut list = div()
            .id("all-assistants")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col();
        for s in spaces {
            for a in s.assistants["assistants"].as_array().into_iter().flatten() {
                let owner = s.owner(a["id"].as_str().unwrap_or("main"));
                let name = a["name"].as_str().unwrap_or("Pot").to_owned();
                let label = if names.iter().filter(|n| **n == name).count() > 1 {
                    format!("{name} · {}", s.name)
                } else {
                    name
                };
                list = list.child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .py_2()
                        .border_b_1()
                        .border_color(cx.theme().border)
                        .child(
                            Button::new(SharedString::from(format!(
                                "assistant-open-{}",
                                owner.key()
                            )))
                            .ghost()
                            .label(label)
                            .flex_1()
                            .on_click(cx.listener(
                                move |this, _, window, cx| {
                                    if this.activate_owner(&owner, window, cx) {
                                        this.assistant_list = false;
                                        this.assistants_ui.open = false;
                                        this.show_tasks = false;
                                        ui::command(json!({"action":"page","value":0}));
                                    }
                                    cx.notify();
                                },
                            )),
                        )
                        .child(badge(&s, a["id"].as_str().unwrap_or("main")))
                        .when(!s.error.is_empty(), |el| {
                            el.child(ui::hint(
                                crate::i18n::text(if s.connected {
                                    "spaces.stale"
                                } else {
                                    "machines.offline"
                                }),
                                cx,
                            ))
                        }),
                );
            }
        }
        if self.space_directory.spaces.is_empty() {
            list = list.child(ui::hint(crate::i18n::text("client.loading"), cx));
        }
        body = body.child(list);
        let error = self.space_directory.error();
        if !error.is_empty() {
            body = body.child(ui::notice(error, true, cx));
        }
        ui::page(cx).child(body)
    }
    pub fn usage_breakdown(&self, cx: &mut Context<Self>) -> Div {
        let query = (
            self.usage_ui.days,
            self.native["timezone"]
                .as_str()
                .unwrap_or("Asia/Shanghai")
                .to_owned(),
        );
        div()
            .ml(px(56.))
            .px_8()
            .flex()
            .flex_wrap()
            .gap_4()
            .children(
                self.space_directory
                    .spaces
                    .values()
                    .filter(|s| self.usage_location.is_empty() || self.usage_location == s.mode)
                    .map(|s| {
                        let value = if s.usage_query.as_ref() == Some(&query) {
                            format!(
                                "{} tokens",
                                crate::usage_ui::count(
                                    s.usage["total"]["input"].as_u64().unwrap_or(0)
                                        + s.usage["total"]["output"].as_u64().unwrap_or(0)
                                )
                            )
                        } else {
                            crate::i18n::text("client.loading").into()
                        };
                        ui::hint(
                            format!(
                                "{} · {value}{}",
                                s.label(),
                                if s.error.is_empty() { "" } else { " · ⚠" }
                            ),
                            cx,
                        )
                    }),
            )
    }
    pub fn location_bar(&self, purpose: &'static str, cx: &mut Context<Self>) -> Div {
        let mut row = div()
            .px_8()
            .py_2()
            .flex()
            .items_center()
            .gap_2()
            .child(ui::hint(crate::i18n::text("spaces.location"), cx));
        if purpose == "usage" {
            row = row.child(
                Button::new("usage-all-spaces")
                    .ghost()
                    .small()
                    .label(crate::i18n::text("spaces.all"))
                    .toggled(self.usage_location.is_empty())
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.usage_location.clear();
                        this.usage_ui.group_offset = 0;
                        cx.notify();
                    })),
            );
        }
        for space in self.space_directory.spaces.values() {
            let owner = space.owner("main");
            let selected = if purpose == "usage" {
                self.usage_location == space.mode
            } else {
                backend::mode() == space.mode
            };
            row = row.child(
                Button::new(SharedString::from(format!(
                    "location-{purpose}-{}",
                    space.id
                )))
                .ghost()
                .small()
                .label(space.label())
                .toggled(selected)
                .disabled(purpose != "usage" && !space.error.is_empty())
                .on_click(cx.listener(move |this, _, window, cx| {
                    if purpose == "usage" {
                        this.usage_location = owner.mode.clone();
                        this.usage_ui.group_offset = 0;
                    } else {
                        let section = this.native["settings"]["section"].clone();
                        if purpose == "settings" && this.settings_return.is_none() {
                            this.settings_return = this.capture_chat();
                        }
                        if backend::mode() != owner.mode {
                            this.switch_mode(window, cx);
                            if purpose == "create" {
                                this.assistants_ui.form_only = true;
                                this.assistants_ui.select("new");
                            }
                        }
                        if purpose == "settings" {
                            ui::command(json!({"action":"section","value":section}));
                            ui::command(json!({"action":"page","value":2}));
                        }
                    }
                    cx.notify();
                })),
            );
        }
        row
    }
    pub fn render_spaces_machines(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let view = self.client.view.lock().unwrap().clone();
        let base = self.page_handler(cx);
        let connections = self.machine_connections.render(
            pot_ui::machine_connections::View {
                client: Some(&backend::control_projection()),
                directory: &view.machines,
                selected: &self.machine_selection,
                reply: &view.machine_reply,
                pending: view.machine_pending,
            },
            self.catalog(),
            base.clone(),
            window,
            cx,
        );
        let mut body = div()
            .px_8()
            .py_4()
            .id("spaces-machines")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .child(connections);
        for space in self.space_directory.spaces.values() {
            let base = base.clone();
            let source = space.clone();
            let permission_owner = if space.mode == view.client_mode {
                view.assistant_id().to_owned()
            } else {
                "main".into()
            };
            let permission_book = space
                .permissions
                .get(&permission_owner)
                .cloned()
                .unwrap_or(Value::Null);
            let selected = if self.machine_selection["space_id"] == space.id {
                self.machine_selection["execution_target"].clone()
            } else {
                Value::Null
            };
            let handler: pot_ui::pages::Handler = std::rc::Rc::new(cx.listener(
                move |this, event: &pot_ui::pages::PageAction, window, cx| {
                    if event.kind == "machine-select" {
                        this.machine_selection =
                            json!({"space_id":source.id,"execution_target":event.value});
                        cx.notify();
                        return;
                    }
                    if event.kind == "execution-target-tasks" {
                        this.client.filter_execution_target(
                            json!({"space_id":source.id,"execution_target":event.value})
                                .to_string(),
                        );
                        this.task_search
                            .update(cx, |f, cx| f.set_value("", window, cx));
                        this.show_tasks = true;
                        this.schedule_tab = false;
                        ui::command(json!({"action":"page","value":3}));
                        cx.notify();
                        return;
                    }
                    if event.kind == "permission-decision" {
                        if !source.error.is_empty() {
                            return;
                        }
                        let owner = source.owner(&permission_owner);
                        let value = event.value.clone();
                        std::thread::spawn(move || {
                            let result = backend::Requests::for_mode(&owner.mode)
                                .scope(&owner.assistant)
                                .post("/api/v1/permissions")
                                .send_json(value);
                            if let Err(error) = result
                                && let Some(s) = crate::spaces::directory()
                                    .lock()
                                    .unwrap()
                                    .spaces
                                    .get_mut(&owner.mode)
                            {
                                s.error = error;
                            }
                            crate::wake_ui();
                        });
                        return;
                    }
                    base(event, window, cx);
                },
            ));
            body = body.child(
                div()
                    .pt_4()
                    .border_t_1()
                    .border_color(cx.theme().border)
                    .child(ui::section(space.label()))
                    .when(!space.error.is_empty(), |el| {
                        el.child(ui::notice(
                            format!("{} · {}", crate::i18n::text("spaces.stale"), space.error),
                            true,
                            cx,
                        ))
                    })
                    .child(pot_ui::machines::content(
                        pot_ui::machines::MachinesView {
                            directory: &space.machines,
                            permissions: if space.error.is_empty() {
                                &permission_book
                            } else {
                                &Value::Null
                            },
                            preferred: &selected,
                            desktop_runtime: if space.mode == "local" {
                                Some(&space.id)
                            } else {
                                None
                            },
                        },
                        self.catalog(),
                        handler,
                        cx,
                    )),
            );
        }
        if backend::control_projection()["configured"] != true {
            body = body.child(
                div()
                    .pt_4()
                    .border_t_1()
                    .border_color(cx.theme().border)
                    .child(ui::section(crate::i18n::text("spaces.cloud")))
                    .child(
                        Button::new("cloud-connect")
                            .ghost()
                            .label(crate::i18n::text("onboarding.connect-server"))
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.machine_connections.open = true;
                                cx.notify();
                            })),
                    ),
            );
        }
        ui::page(cx).flex().flex_col().child(body)
    }
    pub fn render_space_schedules(&self, cx: &mut Context<Self>) -> Div {
        let mut entries = vec![];
        for s in self.space_directory.spaces.values() {
            for (assistant, p) in &s.schedules {
                entries.push((s.clone(), s.owner(assistant), p.clone()));
            }
        }
        entries.sort_by(|a, b| {
            b.2.created_at_ms
                .cmp(&a.2.created_at_ms)
                .then_with(|| a.1.object(&a.2.id).cmp(&b.1.object(&b.2.id)))
        });
        let total = entries.len();
        let offset = self
            .client
            .view
            .lock()
            .unwrap()
            .schedule_offset
            .min(total.saturating_sub(1) / 20 * 20);
        let mut list = div()
            .id("spaces-schedules")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll();
        for (space, owner, plan) in entries.into_iter().skip(offset).take(20) {
            let name = space.assistants["assistants"]
                .as_array()
                .into_iter()
                .flatten()
                .find(|a| a["id"] == owner.assistant)
                .and_then(|a| a["name"].as_str())
                .unwrap_or("Pot")
                .to_owned();
            let id = plan.id.clone();
            list = list.child(
                ui::card(cx)
                    .child(
                        Button::new(SharedString::from(owner.object(&id)))
                            .ghost()
                            .label(plan.spec.title)
                            .on_click(cx.listener(move |this, _, window, cx| {
                                if this.activate_owner(&owner, window, cx) {
                                    this.client.schedule_select(id.clone());
                                    this.show_tasks = true;
                                    this.schedule_tab = true;
                                    ui::command(json!({"action":"page","value":3}));
                                }
                                cx.notify();
                            })),
                    )
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(name)
                            .child(badge(&space, &plan.id)),
                    )
                    .child(ui::hint(
                        if space.error.is_empty() {
                            plan.next_label
                        } else {
                            crate::i18n::text("spaces.stale").into()
                        },
                        cx,
                    )),
            );
        }
        ui::page(cx).child(
            ui::content()
                .child(crate::schedules_ui::navigation(true, cx))
                .child(list)
                .child(ui::hint(format!("{total} · {}", offset / 20 + 1), cx))
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(
                            Button::new("all-schedules-prev")
                                .ghost()
                                .label(crate::i18n::text("schedules-ui.previous"))
                                .disabled(offset == 0)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.client.view.lock().unwrap().schedule_offset =
                                        offset.saturating_sub(20);
                                    cx.notify();
                                })),
                        )
                        .child(
                            Button::new("all-schedules-next")
                                .ghost()
                                .label(crate::i18n::text("schedules-ui.next-52"))
                                .disabled(offset + 20 >= total)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.client.view.lock().unwrap().schedule_offset = offset + 20;
                                    cx.notify();
                                })),
                        ),
                )
                .when(!self.space_directory.error().is_empty(), |el| {
                    el.child(ui::notice(self.space_directory.error(), true, cx))
                }),
        )
    }
}
