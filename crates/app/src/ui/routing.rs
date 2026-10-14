use super::*;
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

impl Settings {
    pub(super) fn routing_form(&self, state: &Value, _cx: &mut Context<Shell>) -> Form {
        let busy = state["routing_busy"] == true;
        let selected = state["routing_profile"].as_str().unwrap_or("");
        let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
        if profiles.is_empty() {
            return form(_cx)
                .child(
                    field(crate::i18n::text("routing.background-task-agent"))
                        .child(hint(crate::i18n::text("routing.follow-chat"), _cx)),
                )
                .child(
                    field(crate::i18n::text("routing.no-configurations-yet"))
                        .description(crate::i18n::text(
                            "routing.add-an-agent-in-ai-configuration-first",
                        ))
                        .child(
                            Button::new("routing-connect-agent")
                                .small()
                                .max_w(px(240.))
                                .label(crate::i18n::text("ai.connect-agent-58"))
                                .on_click(|_, _, _| command(json!({"action":"section","value":1}))),
                        ),
                );
        }
        let mut form = form(_cx).child(
            field(crate::i18n::text("routing.saved-configurations")).child(
                Button::new("routing-refresh")
                    .ghost()
                    .small()
                    .max_w(px(260.))
                    .justify_start()
                    .label(crate::i18n::text("routing.refresh-discard-changes"))
                    .disabled(busy)
                    .on_click(|_, _, _| command(json!({"action":"routing-refresh"}))),
            ),
        );
        let independent = state["routing"]["worker_profile"].as_str().is_some();
        let worker_choices = profiles.clone();
        let worker_label = profiles
            .iter()
            .find(|p| p["id"] == state["routing"]["worker_profile"])
            .map(|p| {
                format!(
                    "{} · {}",
                    p["name"].as_str().unwrap_or("Agent"),
                    p["model"].as_str().unwrap_or("")
                )
            })
            .unwrap_or_else(|| crate::i18n::text("routing.choose-an-independent-agent").into());
        let details_label = profiles
            .iter()
            .find(|p| p["id"] == selected)
            .and_then(|p| p["name"].as_str())
            .unwrap_or(crate::i18n::text("routing.choose-an-agent-to-view"))
            .to_owned();
        form = form.child(
            field(crate::i18n::text("routing.background-task-agent"))
                .description(crate::i18n::text(
                    "routing.follows-chat-by-default-independent-selection-affects",
                ))
                .child(
                    Button::new("worker-follow-chat")
                        .small()
                        .max_w(px(260.))
                        .self_start()
                        .label(if independent {
                            crate::i18n::text("routing.switch-to-following-chat")
                        } else {
                            crate::i18n::text("routing.follow-chat")
                        })
                        .toggled(!independent)
                        .disabled(busy)
                        .on_click(|_, _, _| {
                            command(json!({"action":"routing-choice","kind":"worker","id":""}))
                        }),
                ),
        );
        if state["routing_owner"] == "main" {
            form = form.child(field(crate::i18n::text("routing.independent-task-configuration")).child(Button::new("worker-pick").small().self_start().max_w(px(320.)).label(worker_label).dropdown_caret(true).disabled(busy)
                .dropdown_menu(move|menu,_,_| worker_choices.iter().fold(menu,|menu,p| {let id=p["id"].as_str().unwrap_or("").to_owned();
                    menu.item(PopupMenuItem::new(p["name"].as_str().unwrap_or("Agent").to_owned()).on_click(move|_,_,_|command(json!({"action":"routing-choice","kind":"worker","id":id}))))}))));
        }
        form = form.child(
            field(crate::i18n::text("routing.configuration-details")).child(
                Button::new("routing-details")
                    .small()
                    .self_start()
                    .max_w(px(320.))
                    .label(details_label)
                    .dropdown_caret(true)
                    .disabled(busy)
                    .dropdown_menu({
                        let profiles = profiles.clone();
                        move |menu, _, _| {
                            profiles.iter().fold(menu, |menu, p| {
                                let id = p["id"].as_str().unwrap_or("").to_owned();
                                menu.item(
                                    PopupMenuItem::new(
                                        p["name"].as_str().unwrap_or("Agent").to_owned(),
                                    )
                                    .on_click(
                                        move |_, _, _| {
                                            command(json!({"action":"routing-edit","id":id}))
                                        },
                                    ),
                                )
                            })
                        }
                    }),
            ),
        );
        if profiles.is_empty() {
            return form.child(
                field(crate::i18n::text("routing.no-configurations-yet")).child(crate::i18n::text(
                    "routing.add-an-agent-in-ai-configuration-first",
                )),
            );
        }
        if state["routing_owner"] != "main" {
            let id = selected.to_owned();
            form = form.child(
                field(crate::i18n::text("routing.connection-and-sign-in")).child(
                    Button::new("task-ai-manage")
                        .small()
                        .label(crate::i18n::text(
                            "routing.manage-the-selected-agent-connection",
                        ))
                        .on_click(move |_, _, _| command(json!({"action":"ai-manage","id":id}))),
                ),
            );
        }
        form = form.child(
            field(crate::i18n::text("routing.advanced-routing")).child(
                Button::new("routing-advanced")
                    .ghost()
                    .small()
                    .label(if self.advanced {
                        crate::i18n::text("routing.hide-advanced-routing")
                    } else {
                        crate::i18n::text(
                            "routing.show-advanced-routing-permissions-and-allowances",
                        )
                    })
                    .on_click(_cx.listener(|this, _, _, cx| {
                        this.settings.advanced = !this.settings.advanced;
                        cx.notify();
                    })),
            ),
        );
        if !self.advanced && state["routing_owner"] == "main" {
            let fields = self.fields.clone();
            return form.child(field(crate::i18n::text("ui.task-configuration")).child(Button::new("routing-save-simple").primary().small().label(crate::i18n::text("routing.save-task-configuration")).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"routing-save","profile_limit":fields[19].read(cx).value(),"provider_limit":fields[20].read(cx).value()})))));
        }
        for (i, p) in profiles.iter().enumerate() {
            let id = p["id"].as_str().unwrap_or("").to_owned();
            let allowed = state["routing"]["allowed_tool_profiles"]
                .as_array()
                .is_some_and(|a| a.iter().any(|p| p == &id));
            form = form.child(
                field(p["name"].as_str().unwrap_or("Agent").to_owned()).child(
                    Button::new(("routing-hint", i))
                        .ghost()
                        .small()
                        .label(if allowed {
                            crate::i18n::text("routing.allow-agent-selection")
                        } else {
                            crate::i18n::text("routing.allow-agent-selection-20")
                        })
                        .disabled(busy)
                        .on_click(move |_, _, _| {
                            command(json!({"action":"routing-choice","kind":"hint","id":id}))
                        }),
                ),
            );
        }
        for (index, label, description) in [
            (
                17,
                crate::i18n::text("routing.configuration-name"),
                crate::i18n::text("routing.save-this-configuration-or-copy-it-independently"),
            ),
            (
                18,
                crate::i18n::text("ai.model"),
                crate::i18n::text("routing.use-a-model-id-supported-by-this"),
            ),
            (
                19,
                crate::i18n::text("routing.profile-concurrency-limit"),
                crate::i18n::text("routing.1-1000-leave-blank-to-use-only"),
            ),
            (
                20,
                crate::i18n::text("routing.provider-concurrency-limit"),
                crate::i18n::text("routing.shared-by-profiles-for-the-same-provider"),
            ),
        ] {
            form = form.child(
                field(label)
                    .description(description)
                    .child(Input::new(&self.fields[index]).aria_label(label)),
            );
        }
        form = form.child(
            field(crate::i18n::text("routing.unattended-permissions")).child(
                div()
                    .w_full()
                    .min_w_0()
                    .whitespace_normal()
                    .child(crate::i18n::text(
                        "routing.agents-can-operate-files-and-commands-inside",
                    )),
            ),
        );
        form = form.child(
            field(crate::i18n::text("routing.migration-target")).child(
                state["routing_owner_name"]
                    .as_str()
                    .unwrap_or(crate::i18n::text("routing.main-conversation"))
                    .to_owned(),
            ),
        );
        let quota = &state["provider_allowance"];
        if quota["enforcement"] == "pre_request" {
            let mut periods = div().flex().gap_2();
            for (i, (period, name)) in [
                ("none", crate::i18n::text("usage-ui.unlimited")),
                ("day", crate::i18n::text("schedules-ui.daily")),
                ("week", crate::i18n::text("schedules-ui.weekly")),
            ]
            .into_iter()
            .enumerate()
            {
                periods = periods.child(
                    Button::new(("provider-period", i))
                        .ghost()
                        .small()
                        .small()
                        .label(name)
                        .toggled(state["provider_period"] == period)
                        .disabled(busy)
                        .on_click(move |_, _, _| {
                            command(json!({"action":"provider-period","period":period}))
                        }),
                );
            }
            form = form
                .child(field(crate::i18n::text("routing.provider-allowance-period")).child(periods))
                .child(
                    field(crate::i18n::text("routing.provider-token-allowance"))
                        .description(crate::i18n::tr!(
                            "routing.applies-alongside-the-global-allowance-time-zone",
                            quota["policy"]["timezone"]
                                .as_str()
                                .unwrap_or("Asia/Shanghai"),
                            quota["used"].as_u64().unwrap_or(0),
                            quota["reserved"].as_u64().unwrap_or(0)
                        ))
                        .child(
                            Input::new(&self.fields[21])
                                .aria_label(crate::i18n::text("routing.provider-token-allowance")),
                        ),
                )
                .child(
                    field(crate::i18n::text("routing.allowance-reset"))
                        .description(crate::i18n::text(
                            "routing.automatic-resume-also-requires-safe-checkpoint-support",
                        ))
                        .child(
                            Button::new("provider-auto")
                                .ghost()
                                .small()
                                .small()
                                .label(if state["provider_auto_resume"] == true {
                                    crate::i18n::text("routing.resume-automatically")
                                } else {
                                    crate::i18n::text("routing.resume-automatically-38")
                                })
                                .disabled(busy)
                                .on_click(|_, _, _| command(json!({"action":"provider-auto"}))),
                        ),
                );
            let fields = self.fields.clone();
            form = form.child(field(crate::i18n::text("routing.provider-allowance")).child(Button::new("provider-save").small().label(crate::i18n::text("routing.save-provider-allowance")).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"provider-save","tokens":fields[21].read(cx).value()})))));
        } else {
            form = form.child(
                field(crate::i18n::text("routing.provider-token-allowance")).child(
                    div()
                        .w_full()
                        .min_w_0()
                        .whitespace_normal()
                        .child(crate::i18n::text(
                            "routing.cli-sign-in-routes-count-natively-reported",
                        )),
                ),
            );
        }
        let mut actions = div().flex().flex_wrap().gap_2();
        for (i, copy) in [false, true].into_iter().enumerate() {
            let fields = self.fields.clone();
            actions = actions.child(Button::new(("save-profile",i)).small().label(if copy {crate::i18n::text("routing.copy-as-new-configuration")}else{crate::i18n::text("routing.save-configuration")}).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"profile-save","copy":copy,"name":fields[17].read(cx).value(),"model":fields[18].read(cx).value()}))));
        }
        if state["routing_owner"] != "main" {
            actions = actions.child(
                Button::new("profile-main")
                    .small()
                    .label(crate::i18n::text(
                        "routing.save-and-change-this-task-s-agent",
                    ))
                    .disabled(busy)
                    .on_click(|_, _, _| command(json!({"action":"profile-main"}))),
            );
        }
        let fields = self.fields.clone();
        form.child(field(crate::i18n::text("routing.configuration-actions")).child(actions))
            .child(field(crate::i18n::text("routing.task-routing")).child(Button::new("save-routing").primary().small().small().label(crate::i18n::text("routing.save-defaults-allowlist-and-concurrency-limits")).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"routing-save","profile_limit":fields[19].read(cx).value(),"provider_limit":fields[20].read(cx).value()})))))
    }
}
