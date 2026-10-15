use crate::application::{
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

pub fn badge(space: &Space, context: &str) -> Stateful<Div> {
    pot_ui::pages::action_tooltip(
        SharedString::from(format!("space-tip-{}-{context}", space.id)),
        space.label(),
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
        .accessibility_label(space.label()),
    )
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
                crate::application::i18n::text("spaces.identity-changed").into();
            return false;
        }
        let query = self.client.view.lock().unwrap().task_query.clone();
        if backend::mode() != owner.mode {
            self.switch_mode(window, cx);
        }
        self.client.select_assistant(owner.assistant.clone());
        if self.client.view.lock().unwrap().assistant_id() != owner.assistant {
            self.client.view.lock().unwrap().command_error =
                crate::application::i18n::text("spaces.wait-send").into();
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
                                crate::application::i18n::text("permissions.request")
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
                crate::application::task_phase(&task.phase)
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
                    .tooltip(crate::application::i18n::text("assistant.close"))
                    .on_click(cx.listener(|this, _, _, cx| {
                        this.notifications.remove(0);
                        cx.notify();
                    })),
            );
        }
        row
    }
    pub fn collaboration_updates(
        &self,
        view: &crate::application::client::View,
        cx: &mut Context<Self>,
    ) -> Div {
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
        for record in
            crate::application::spaces::chat_updates(records, &space.owner(view.assistant_id()))
        {
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
                            .child(format!(
                                "{} · {name}",
                                crate::application::i18n::text("spaces.reply")
                            ))
                            .when_some(peer, |el, s| el.child(badge(s, &record.message.id))),
                    )
                    .child(div().whitespace_normal().child(record.message.text)),
            );
        }
        body
    }
    pub fn render_assistant_list(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let mut body = ui::content(cx).child(
            div()
                .flex()
                .justify_between()
                .items_center()
                .child(ui::hint(
                    crate::application::i18n::text("spaces.assistants"),
                    cx,
                ))
                .child(
                    Button::new("assistant-new-top")
                        .primary()
                        .small()
                        .icon(IconName::Plus)
                        .label(crate::application::i18n::text("assistant.new"))
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
                            .accessibility_label(label.clone())
                            .child(
                                div()
                                    .w_full()
                                    .flex()
                                    .items_center()
                                    .gap_3()
                                    .child(gpui_kit::component::Icon::new(
                                        gpui_kit::assets::IconName::MessageCircle,
                                    ))
                                    .child(label),
                            )
                            .h(px(52.))
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
                                crate::application::i18n::text(if s.connected {
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
            list = list.child(ui::hint(
                crate::application::i18n::text("client.loading"),
                cx,
            ));
        }
        body = body.child(list);
        let error = self.space_directory.error();
        if !error.is_empty() {
            body = body.child(ui::notice(error, true, cx));
        }
        ui::page(cx).child(body)
    }
    pub fn location_bar(&self, purpose: &'static str, cx: &mut Context<Self>) -> Div {
        let mut row = div()
            .w_full()
            .max_w(px(pot_ui::pages::PAGE_WIDTH))
            .mx_auto()
            .px_8()
            .py_2()
            .flex()
            .items_center()
            .gap_2()
            .child(ui::hint(
                crate::application::i18n::text("spaces.location"),
                cx,
            ));
        if purpose == "usage" {
            row = row.child(
                Button::new("usage-all-spaces")
                    .ghost()
                    .small()
                    .label(crate::application::i18n::text("spaces.all"))
                    .toggled(self.usage_location.is_empty())
                    .when(self.usage_location.is_empty(), |b| {
                        b.bg(pot_ui::design::navigation(cx))
                    })
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
                .when(selected, |b| b.bg(pot_ui::design::navigation(cx)))
                .disabled(purpose != "usage" && !space.error.is_empty())
                .on_click(cx.listener(move |this, _, window, cx| {
                    if purpose == "usage" {
                        this.usage_location = owner.mode.clone();
                        this.usage_ui.group_offset = 0;
                    } else {
                        let section = this.host["settings"]["section"].clone();
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
        if purpose == "settings" || purpose == "create" {
            row.w_auto().mx_0().p_0()
        } else {
            div().pl(px(56.)).flex_shrink_0().child(row)
        }
    }
    pub fn render_spaces_machines(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let view = self.client.view.lock().unwrap().clone();
        let base = self.page_handler(cx);
        let connection_mode = self.machine_connections.mode.clone();
        let connection_directory = connection_mode
            .as_ref()
            .and_then(|mode| self.space_directory.spaces.get(mode))
            .map(|space| space.machines.clone())
            .unwrap_or_else(|| view.machines.clone());
        let connection_base = base.clone();
        let connection_handler: pot_ui::pages::Handler =
            std::rc::Rc::new(move |event, window, cx| {
                let mut event = event.clone();
                if event.kind == "machine-command"
                    && let Some(mode) = &connection_mode
                {
                    event.value["mode"] = json!(mode);
                }
                connection_base(&event, window, cx);
            });
        let control_projection = backend::control_projection();
        let connections = self.machine_connections.render(
            pot_ui::machine_connections::View {
                client: if super::platform::with(|p| p.capabilities().control_switching) {
                    Some(&control_projection)
                } else {
                    None
                },
                directory: &connection_directory,
                selected: &self.machine_selection,
                reply: &view.machine_reply,
                pending: view.machine_pending,
            },
            self.catalog(),
            connection_handler,
            window,
            cx,
        );
        let mut body = div()
            .id("spaces-machines")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_5();
        if self.space_directory.spaces.is_empty() {
            body = body.child(ui::card(cx).child(ui::empty(
                IconName::Inbox,
                crate::application::i18n::text("machines.loading"),
                cx,
            )));
        }
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
                    if event.kind == "machine-labels" {
                        this.machine_connections.mode = Some(source.mode.clone());
                        this.machine_connections
                            .edit_labels(event.value.as_str().unwrap_or_default().to_owned());
                        this.client.view.lock().unwrap().machine_reply = Value::Null;
                        cx.notify();
                        return;
                    }
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
                        super::platform::with(|p| p.permission(&owner, value));
                        return;
                    }
                    base(event, window, cx);
                },
            ));
            body = body.child(
                div()
                    .flex()
                    .flex_col()
                    .gap_3()
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .justify_between()
                            .child(ui::hint(space.label(), cx))
                            .when(
                                space.mode == "remote"
                                    && space.machines["control_authority"]["role"] == "control",
                                |el| {
                                    let mode = space.mode.clone();
                                    el.child(
                                        Button::new("add-execution-machine")
                                            .small()
                                            .icon(IconName::Plus)
                                            .disabled(!space.error.is_empty())
                                            .label(crate::application::i18n::text(
                                                "machines.add-executor",
                                            ))
                                            .on_click(cx.listener(move |this, _, _, cx| {
                                                this.machine_connections.mode = Some(mode.clone());
                                                this.machine_connections.manage_members();
                                                this.client.view.lock().unwrap().machine_reply =
                                                    Value::Null;
                                                cx.notify();
                                            })),
                                    )
                                },
                            ),
                    )
                    .when(!space.error.is_empty(), |el| {
                        el.child(ui::notice(
                            format!(
                                "{} · {}",
                                crate::application::i18n::text("spaces.stale"),
                                space.error
                            ),
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
                            desktop_runtime: if space.mode == "local"
                                || super::platform::with(|p| p.capabilities().browser_access)
                            {
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
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(ui::hint(crate::application::i18n::text("spaces.cloud"), cx))
                    .child(
                        Button::new("cloud-connect")
                            .small()
                            .label(crate::application::i18n::text("onboarding.connect-server"))
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.machine_connections.connect();
                                cx.notify();
                            })),
                    ),
            );
        }
        ui::page(cx)
            .relative()
            .child(
                ui::content(cx)
                    .child(
                        div()
                            .flex()
                            .items_center()
                            .justify_between()
                            .flex_shrink_0()
                            .child(ui::hint(
                                crate::application::i18n::text("machines.title"),
                                cx,
                            )),
                    )
                    .child(body),
            )
            .when(self.machine_connections.open, |el| el.child(connections))
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
            .overflow_y_scroll()
            .when(total == 0, |el| {
                el.child(ui::empty(
                    IconName::Inbox,
                    crate::application::i18n::text(
                        "schedules-ui.no-schedules-yet-tell-pot-what-to",
                    ),
                    cx,
                ))
            });
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
                            crate::application::i18n::text("spaces.stale").into()
                        },
                        cx,
                    )),
            );
        }
        ui::page(cx).child(
            ui::content(cx)
                .child(crate::application::schedules_ui::navigation(true, cx))
                .child(list)
                .when(total > 0, |el| {
                    el.child(ui::hint(format!("{total} · {}", offset / 20 + 1), cx))
                })
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(
                            Button::new("all-schedules-prev")
                                .when(total == 0, |el| el.invisible())
                                .ghost()
                                .label(crate::application::i18n::text("schedules-ui.previous"))
                                .disabled(offset == 0)
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.client.view.lock().unwrap().schedule_offset =
                                        offset.saturating_sub(20);
                                    cx.notify();
                                })),
                        )
                        .child(
                            Button::new("all-schedules-next")
                                .when(total == 0, |el| el.invisible())
                                .ghost()
                                .label(crate::application::i18n::text("schedules-ui.next-52"))
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
