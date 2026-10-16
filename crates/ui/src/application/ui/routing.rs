use super::*;
use crate::pages::{Control, FieldHelp};
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

impl Settings {
    pub(super) fn routing_form(&self, state: &Value, cx: &mut Context<Shell>) -> Form {
        let busy = state["routing_busy"] == true;
        let selected = state["routing_profile"].as_str().unwrap_or("");
        let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
        if profiles.is_empty() {
            return form(cx)
                .child(
                    field(crate::application::i18n::text("ui.task-configuration")).child(hint(
                        crate::application::i18n::text("routing.autonomous"),
                        cx,
                    )),
                )
                .child(
                    field(crate::application::i18n::text(
                        "routing.no-configurations-yet",
                    ))
                    .help(crate::application::i18n::text(
                        "ai.no-agents-connected-yet-choose-an-ai",
                    ))
                    .child(
                        Button::new("routing-connect-agent")
                            .control(cx)
                            .max_w(px(240.))
                            .label(crate::application::i18n::text("ai.connect-agent-58"))
                            .on_click(|_, _, _| command(json!({"action":"section","value":1}))),
                    ),
                );
        }
        let mut form = form(cx).child(
            field(crate::application::i18n::text(
                "routing.saved-configurations",
            ))
            .child(
                Button::new("routing-refresh")
                    .ghost()
                    .control(cx)
                    .max_w(px(260.))
                    .justify_start()
                    .label(crate::application::i18n::text(
                        "routing.refresh-discard-changes",
                    ))
                    .disabled(busy)
                    .on_click(|_, _, _| command(json!({"action":"routing-refresh"}))),
            ),
        );
        form = form
            .child(
                field(crate::application::i18n::text(
                    "routing.configuration-details",
                ))
                .child(
                    Button::new("routing-details")
                        .control(cx)
                        .label(
                            profiles
                                .iter()
                                .find(|p| p["id"] == selected)
                                .and_then(|p| p["name"].as_str())
                                .unwrap_or("")
                                .to_owned(),
                        )
                        .dropdown_caret(true)
                        .disabled(busy)
                        .dropdown_menu(move |menu, _, _| {
                            profiles.iter().fold(menu, |menu, p| {
                                let id = p["id"].as_str().unwrap_or("").to_owned();
                                menu.item(
                                    PopupMenuItem::new(p["name"].as_str().unwrap_or("").to_owned())
                                        .on_click(move |_, _, _| {
                                            command(json!({"action":"routing-edit","id":id}))
                                        }),
                                )
                            })
                        }),
                ),
            )
            .child(field(crate::application::i18n::text("routing.autonomous")));
        for (index, label) in [
            (19, "routing.profile-concurrency-limit"),
            (20, "routing.provider-concurrency-limit"),
        ] {
            form = form.child(
                field(crate::application::i18n::text(label))
                    .child(Input::new(&self.fields[index]).control(cx)),
            );
        }
        let quota = &state["provider_allowance"];
        if quota["enforcement"] == "pre_request" {
            let mut periods = div().flex().gap_2();
            for (i, (period, name)) in [
                ("none", crate::application::i18n::text("usage-ui.unlimited")),
                ("day", crate::application::i18n::text("schedules-ui.daily")),
                (
                    "week",
                    crate::application::i18n::text("schedules-ui.weekly"),
                ),
            ]
            .into_iter()
            .enumerate()
            {
                periods = periods.child(
                    Button::new(("provider-period", i))
                        .ghost()
                        .control(cx)
                        .control(cx)
                        .label(name)
                        .toggled(state["provider_period"] == period)
                        .disabled(busy)
                        .on_click(move |_, _, _| {
                            command(json!({"action":"provider-period","period":period}))
                        }),
                );
            }
            form = form
                .child(
                    field(crate::application::i18n::text(
                        "routing.provider-allowance-period",
                    ))
                    .child(periods),
                )
                .child(
                    field(crate::application::i18n::text(
                        "routing.provider-token-allowance",
                    ))
                    .help(crate::application::i18n::tr!(
                        "routing.applies-alongside-the-global-allowance-time-zone",
                        quota["policy"]["timezone"]
                            .as_str()
                            .unwrap_or("Asia/Shanghai"),
                        quota["used"].as_u64().unwrap_or(0),
                        quota["reserved"].as_u64().unwrap_or(0)
                    ))
                    .child(
                        Input::new(&self.fields[21]).control(cx).aria_label(
                            crate::application::i18n::text("routing.provider-token-allowance"),
                        ),
                    ),
                )
                .child(
                    field(crate::application::i18n::text("routing.allowance-reset"))
                        .help(crate::application::i18n::text(
                            "routing.automatic-resume-also-requires-safe-checkpoint-support",
                        ))
                        .child(
                            Button::new("provider-auto")
                                .ghost()
                                .control(cx)
                                .control(cx)
                                .label(if state["provider_auto_resume"] == true {
                                    crate::application::i18n::text("routing.resume-automatically")
                                } else {
                                    crate::application::i18n::text(
                                        "routing.resume-automatically-38",
                                    )
                                })
                                .disabled(busy)
                                .on_click(|_, _, _| command(json!({"action":"provider-auto"}))),
                        ),
                );
            let fields = self.fields.clone();
            form = form.child(field(crate::application::i18n::text("routing.provider-allowance")).child(Button::new("provider-save").control(cx).label(crate::application::i18n::text("routing.save-provider-allowance")).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"provider-save","tokens":fields[21].read(cx).value()})))));
        } else {
            form = form.child(
                field(crate::application::i18n::text(
                    "routing.provider-token-allowance",
                ))
                .child(div().w_full().min_w_0().whitespace_normal().child(
                    crate::application::i18n::text(
                        "routing.cli-sign-in-routes-count-natively-reported",
                    ),
                )),
            );
        }
        let fields = self.fields.clone();
        form.child(field(crate::application::i18n::text("ui.task-configuration")).child(
            Button::new("save-capacity").primary().control(cx).label(crate::application::i18n::text("editor.save")).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"routing-save","profile_limit":fields[19].read(cx).value(),"provider_limit":fields[20].read(cx).value()})))
        ))
    }
}
