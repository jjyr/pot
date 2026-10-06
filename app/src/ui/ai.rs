use super::*;
use gpui_kit::component::Icon;
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

pub(crate) fn brand_icon(kind: &str) -> Icon {
    let icon = match kind {
        "codex" | "openai" => Icon::empty().data(include_bytes!("../../assets/agents/openai.svg")),
        "anthropic" => Icon::empty().data(include_bytes!("../../assets/agents/claude.svg")),
        "gemini" => Icon::empty().data(include_bytes!("../../assets/agents/gemini.svg")),
        "deepseek" => Icon::empty().data(include_bytes!("../../assets/agents/deepseek.svg")),
        "opencode-go" | "opencode-zen" => {
            Icon::empty().data(include_bytes!("../../assets/agents/opencode.svg"))
        }
        _ => Icon::new(IconName::Globe),
    };
    icon.text_color(gpui::rgb(match kind {
        "codex" | "openai" => 0x159B7D,
        "anthropic" => 0xD97757,
        "gemini" => 0x4285F4,
        "deepseek" => 0x4D6BFE,
        "opencode-go" => 0x0E9B9B,
        "opencode-zen" => 0x8B5CF6,
        _ => 0x6379A8,
    }))
}

fn brand_name(kind: &str) -> &'static str {
    match kind {
        "codex" => "Codex",
        "anthropic" => "Claude",
        "gemini" => "Gemini",
        "openai" => "OpenAI API",
        "deepseek" => "DeepSeek",
        "opencode-go" => "OpenCode Go",
        "opencode-zen" => "OpenCode Zen",
        _ => crate::i18n::text("ai.custom-api"),
    }
}

fn profile_brand<'a>(state: &'a Value, profile: &'a Value) -> &'a str {
    let engine = profile["engine"].as_str().unwrap_or("");
    if engine != "pot" {
        return engine;
    }
    let endpoint = state["connections"]
        .as_array()
        .and_then(|items| {
            items
                .iter()
                .find(|item| item["profile_id"] == profile["id"])
        })
        .and_then(|item| item["endpoint"].as_str())
        .unwrap_or("");
    if endpoint.starts_with("https://api.openai.com/") {
        "openai"
    } else if endpoint.starts_with("https://api.anthropic.com/") {
        "anthropic"
    } else if endpoint.starts_with("https://generativelanguage.googleapis.com/") {
        "gemini"
    } else if endpoint.starts_with("https://api.deepseek.com/") {
        "deepseek"
    } else if endpoint.starts_with("https://opencode.ai/zen/go/") {
        "opencode-go"
    } else if endpoint.starts_with("https://opencode.ai/zen/") {
        "opencode-zen"
    } else {
        "custom"
    }
}

pub(super) fn agent_status(state: &Value, engine: &str) -> &'static str {
    if engine == "pot" {
        return crate::i18n::text("ai.configured");
    }
    if state["discovering"] == true {
        return crate::i18n::text("ai.checking");
    }
    let agent = state["agents"]
        .as_array()
        .and_then(|a| a.iter().find(|a| a["id"] == engine));
    match agent {
        Some(a) if a["installation"] == "missing" => crate::i18n::text("ai.not-installed"),
        Some(a) if a["status_check"] == "failed" => {
            crate::i18n::text("ai.unable-to-detect-right-now")
        }
        Some(a) if a["authentication"] == "authenticated" => crate::i18n::text("ai.signed-in"),
        Some(a) if a["authentication"] == "required" => crate::i18n::text("ai.sign-in-required"),
        _ => crate::i18n::text("ai.unknown-status"),
    }
}

impl Settings {
    pub(super) fn render_ai(&self, state: &Value, cx: &mut Context<Shell>) -> Div {
        let busy = state["ai_busy"] == true;
        let screen = state["ai_screen"].as_str().unwrap_or("main");
        let mut body = div()
            .id(SharedString::from(format!(
                "ai-scroll-{}-{}",
                screen,
                state["secret_revision"].as_i64().unwrap_or(0)
            )))
            .flex_1()
            .min_h_0()
            .when(state["design_underlay"] != true, |el| {
                el.track_scroll(&self.scroll)
            })
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_4();
        if screen == "add" {
            body = body.child(hint(
                crate::i18n::text("ai.choose-the-ai-you-want-to-use"),
                cx,
            ));
            let mut choices = div().flex().flex_wrap().gap_3();
            for (i, id) in [
                "codex",
                "anthropic",
                "gemini",
                "deepseek",
                "opencode-go",
                "opencode-zen",
                "custom",
            ]
            .into_iter()
            .enumerate()
            {
                choices = choices.child(
                    div().w(relative(0.31)).child(
                        Button::new(("ai-add-kind", i))
                            .w_full()
                            .h(px(52.))
                            .small()
                            .label(brand_name(id))
                            .icon(brand_icon(id).size(px(20.)))
                            .disabled(busy)
                            .on_click(move |_, _, _| command(json!({"action":"ai-new","kind":id}))),
                    ),
                );
            }
            body = body.child(choices);
        } else if screen == "manage" {
            let native = matches!(state["connection_kind"].as_str(), Some("codex"));
            let kind = state["connection_kind"].as_str().unwrap_or("").to_owned();
            let mut form = super::form(cx);
            if (!native && self.api_advanced) || (native && self.commands_open) {
                form = form.child(field(crate::i18n::text("schedules-ui.name")).child(
                    Input::new(&self.fields[23]).aria_label(crate::i18n::text("ai.agent-name")),
                ));
            }
            if native {
                let status = agent_status(state, &kind);
                form = form.child(field(crate::i18n::text("ai.connection-status")).child(status));
                if status != crate::i18n::text("ai.signed-in") {
                    let login_kind = kind.clone();
                    form = form.child(
                        field(crate::i18n::text("ai.account-authorization"))
                            .description(crate::i18n::text(
                                "ai.complete-official-authorization-in-your-browser-pot",
                            ))
                            .child(
                                Button::new("ai-login")
                                    .small()
                                    .label(crate::i18n::text("ai.sign-in"))
                                    .disabled(busy || state["login_visible"] == true)
                                    .on_click(move |_, _, _| {
                                        command(json!({"action":"login-open","engine":login_kind}))
                                    }),
                            ),
                    );
                }
                if state["login_visible"] == true {
                    form = form.child(
                        field(crate::i18n::text("ai.waiting-for-authorization"))
                            .description(state["login_message"].as_str().unwrap_or("").to_owned())
                            .child(
                                div()
                                    .flex()
                                    .gap_2()
                                    .child(
                                        Button::new("ai-login-reopen")
                                            .small()
                                            .label(crate::i18n::text("ai.reopen-browser"))
                                            .on_click(|_, _, _| {
                                                command(json!({"action":"login-reopen"}))
                                            }),
                                    )
                                    .child(
                                        Button::new("ai-login-cancel")
                                            .small()
                                            .label(crate::i18n::text("schedules-ui.cancel"))
                                            .on_click(|_, _, _| {
                                                command(json!({"action":"login-cancel"}))
                                            }),
                                    ),
                            ),
                    );
                }
                form = form.child(
                    field(crate::i18n::text("ai.check-status")).child(
                        Button::new("ai-check-native")
                            .small()
                            .label(crate::i18n::text("ai.refresh-status"))
                            .disabled(busy || state["discovering"] == true)
                            .on_click(|_, _, _| command(json!({"action":"agent-refresh"}))),
                    ),
                );
            } else {
                if kind == "custom" || self.api_advanced {
                    form = form.child(
                        field(crate::i18n::text("ai.api-url"))
                            .description(crate::i18n::text("ai.enter-an-https-base-url-or-full"))
                            .child(
                                Input::new(&self.fields[14])
                                    .aria_label(crate::i18n::text("ai.api-url")),
                            ),
                    );
                }
                if (kind == "custom" || self.api_advanced)
                    && matches!(
                        state["api_protocol"].as_str(),
                        Some("openai_chat" | "openai_responses" | "anthropic_messages" | "gemini")
                    )
                {
                    let mut formats = div().flex().flex_wrap().gap_2();
                    for (i, (protocol, label)) in [
                        ("openai_chat", "OpenAI Chat Completions"),
                        ("openai_responses", "OpenAI Responses"),
                        ("anthropic_messages", "Claude Messages"),
                        ("gemini", "Gemini"),
                    ]
                    .into_iter()
                    .enumerate()
                    {
                        formats = formats.child(
                            Button::new(("ai-protocol", i))
                                .ghost()
                                .small()
                                .label(label)
                                .toggled(state["api_protocol"] == protocol)
                                .when(state["api_protocol"] == protocol, |b| {
                                    b.bg(cx.theme().secondary)
                                })
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"api-protocol","protocol":protocol}))
                                }),
                        );
                    }
                    form = form.child(field(crate::i18n::text("ai.request-format")).child(formats));
                }
                form = form.child(
                    field("API key")
                        .description(if state["has_api_key"] == true {
                            crate::i18n::text("ai.saved-leave-blank-to-keep-the-key")
                        } else {
                            crate::i18n::text("ai.provide-your-service-credentials-and-pot-will")
                        })
                        .child(Input::new(&self.fields[15]).aria_label("API key")),
                );
            }
            let key_url = match kind.as_str() {
                "anthropic" => Some("https://platform.claude.com/settings/keys"),
                "gemini" => Some("https://aistudio.google.com/api-keys"),
                "deepseek" => Some("https://platform.deepseek.com/api_keys"),
                "opencode-go" | "opencode-zen" => Some("https://opencode.ai/auth"),
                "openai" => Some("https://platform.openai.com/api-keys"),
                _ => None,
            };
            if let Some(url) = key_url {
                form = form.child(
                    field("").child(
                        Button::new("ai-get-key")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("ai.get-api-key"))
                            .on_click(move |_, _, cx| cx.open_url(url)),
                    ),
                );
            }
            if !native || agent_status(state, &kind) == crate::i18n::text("ai.signed-in") {
                let mut model_field = div().flex().items_center().gap_2().child(
                    div().flex_1().min_w_0().child(
                        Input::new(&self.fields[16])
                            .aria_label(crate::i18n::text("ai.connection-model")),
                    ),
                );
                if let Some(models) = state["model_options"].as_array().filter(|m| !m.is_empty()) {
                    let models: Vec<String> = models
                        .iter()
                        .filter_map(|m| m.as_str().map(str::to_owned))
                        .collect();
                    let field = self.fields[16].clone();
                    model_field = model_field.child(
                        Button::new("ai-model-options")
                            .small()
                            .label(crate::i18n::text("ai.choose"))
                            .dropdown_caret(true)
                            .dropdown_menu(move |menu, _, _| {
                                models.iter().fold(menu, |menu, model| {
                                    let field = field.clone();
                                    let model = model.clone();
                                    menu.item(PopupMenuItem::new(model.clone()).on_click(
                                        move |_, window, cx| {
                                            field.update(cx, |f, cx| {
                                                f.set_value(model.clone(), window, cx)
                                            })
                                        },
                                    ))
                                })
                            }),
                    );
                }
                form = form.child(
                    field(crate::i18n::text("ai.model"))
                        .description(if native {
                            crate::i18n::text("ai.enter-a-model-supported-by-your-account")
                        } else {
                            crate::i18n::text("ai.choose-a-result-or-enter-a-model")
                        })
                        .child(model_field),
                );
            }
            let fields = self.fields.clone();
            let check_fields = self.fields.clone();
            let mut actions = div().flex().flex_wrap().justify_end().gap_2();
            if !native {
                actions = actions.child(Button::new("ai-api-check").small().label(if busy {crate::i18n::text("ui.working")} else {crate::i18n::text("ai.check-connection")})
                    .disabled(busy).on_click(move |_, _, cx| command(json!({"action":"ai-api-check","endpoint":check_fields[14].read(cx).value(),"key":check_fields[15].read(cx).value()}))));
            }
            actions = actions.child(Button::new("ai-save-connection").primary().small().label(if state["active_spec"]["model"].as_str().is_none() && state["routing_owner"] == "main" {
                    if state["return_to_chat"] == true { crate::i18n::text("ai.save-and-start-chatting") } else { crate::i18n::text("ai.save-and-set-as-main-agent") }
                } else { crate::i18n::text("ai.save-connection") }).disabled(busy || state["login_visible"] == true || (native && agent_status(state, &kind) != crate::i18n::text("ai.signed-in")))
                .on_click(move |_, _, cx| command(json!({"action":"ai-save-connection","name":fields[23].read(cx).value(),"model":fields[16].read(cx).value(),"endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value()}))));
            let mut details = div().flex().flex_col().gap_4();
            details = details.child(form);
            if !native {
                details =
                    details
                        .child(hint(
                            crate::i18n::text("ai.check-to-fetch-available-models-or-save"),
                            cx,
                        ))
                        .when(
                            !state["check_message"].as_str().unwrap_or("").is_empty(),
                            |el| {
                                el.child(div().whitespace_normal().child(
                                    state["check_message"].as_str().unwrap_or("").to_owned(),
                                ))
                            },
                        );
            }
            body = body.child(
                details.child(
                    div()
                        .flex()
                        .items_center()
                        .justify_between()
                        .gap_2()
                        .when(kind == "custom" || native, |row| {
                            row.child(
                                Button::new("ai-advanced")
                                    .ghost()
                                    .small()
                                    .label(crate::i18n::text("ai.advanced-settings"))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        if native {
                                            this.settings.commands_open =
                                                !this.settings.commands_open;
                                        } else {
                                            this.settings.api_advanced =
                                                !this.settings.api_advanced;
                                        }
                                        cx.notify();
                                    })),
                            )
                        })
                        .child(actions),
                ),
            );
            if native && self.commands_open {
                let fields = self.fields.clone();
                body=body.child(card(cx).child(hint(crate::i18n::text("ai.save-the-connection-before-editing-its-command"),cx))
                    .child(Input::new(&self.fields[8]).aria_label(crate::i18n::text("ai.executable")))
                    .child(Input::new(&self.fields[9]).aria_label(crate::i18n::text("ai.additional-arguments-json")))
                    .child(Button::new("ai-save-invocation").small().label(crate::i18n::text("ai.save-command")).disabled(busy || state["connection_name"].is_null())
                        .on_click(move|_,_,cx|command(json!({"action":"save-invocation","executable":fields[8].read(cx).value(),"argv":fields[9].read(cx).value()})))));
            }
        } else {
            let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
            let draft = state["draft_profile"].as_str().unwrap_or("").to_owned();
            let selected = profiles.iter().find(|p| p["id"] == draft);
            let label = selected
                .and_then(|p| p["name"].as_str())
                .unwrap_or(crate::i18n::text("ai.choose-an-agent"))
                .to_owned();
            let options = profiles.clone();
            let model_input = self.fields[22].clone();
            let mut form = super::form(cx)
                .child(
                    field("Agent").child(
                        Button::new("ai-main-picker")
                            .small()
                            .label(label)
                            .dropdown_caret(true)
                            .disabled(busy)
                            .dropdown_menu(move |menu, _, _| {
                                options.iter().fold(menu, |menu, p| {
                                    let id = p["id"].as_str().unwrap_or("").to_owned();
                                    menu.item(
                                        PopupMenuItem::new(
                                            p["name"].as_str().unwrap_or("Agent").to_owned(),
                                        )
                                        .checked(id == draft)
                                        .on_click(
                                            move |_, _, _| {
                                                command(json!({"action":"ai-choose","id":id}))
                                            },
                                        ),
                                    )
                                })
                            }),
                    ),
                )
                .child(
                    field(crate::i18n::text("ai.model"))
                        .description(crate::i18n::text(
                            "ai.enter-a-model-supported-by-this-agent",
                        ))
                        .child(
                            Input::new(&model_input)
                                .aria_label(crate::i18n::text("ai.chat-model"))
                                .disabled(busy),
                        ),
                );
            if let Some(selected) = selected {
                let engine = selected["engine"].as_str().unwrap_or("");
                let status = agent_status(state, engine);
                form = form.child(field(crate::i18n::text("ai.connection-status")).child(status));
                if engine != "pot" && status != crate::i18n::text("ai.signed-in") {
                    let id = selected["id"].as_str().unwrap_or("").to_owned();
                    form = form.child(
                        field(crate::i18n::text("ai.finish-connection")).child(
                            Button::new("ai-complete")
                                .small()
                                .label(crate::i18n::text("ai.sign-in-and-model-settings"))
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"ai-manage","id":id}))
                                }),
                        ),
                    );
                }
            }
            let active = &state["active_spec"];
            if screen == "main" {
                let active_profile = profiles
                    .iter()
                    .find(|p| p["id"] == active["source_profile_id"]);
                body = body.child(
                    card(cx)
                        .child(section(crate::i18n::text("ai.main-agent")))
                        .child(hint(
                            crate::i18n::text("ai.pot-will-use-this-agent-for-the"),
                            cx,
                        ))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap_3()
                                .child(
                                    brand_icon(
                                        active_profile
                                            .map(|p| profile_brand(state, p))
                                            .unwrap_or("custom"),
                                    )
                                    .size(px(20.)),
                                )
                                .child(
                                    div().flex_1().child(
                                        state["active_name"]
                                            .as_str()
                                            .filter(|_| active["model"].as_str().is_some())
                                            .unwrap_or(crate::i18n::text("ai.not-configured"))
                                            .to_owned(),
                                    ),
                                )
                                .child(hint(
                                    active["model"].as_str().unwrap_or(crate::i18n::text(
                                        "ai.connect-an-agent-then-choose-a-model",
                                    )),
                                    cx,
                                ))
                                .child(
                                    Button::new("ai-edit-main")
                                        .small()
                                        .label(if active["model"].as_str().is_some() {
                                            crate::i18n::text("ai.change")
                                        } else {
                                            crate::i18n::text("ui.settings")
                                        })
                                        .disabled(busy || profiles.is_empty())
                                        .on_click(|_, _, _| {
                                            command(
                                                json!({"action":"ai-screen","screen":"main-edit"}),
                                            )
                                        }),
                                ),
                        ),
                );
            } else {
                body = body
                    .child(form)
                    .child(hint(crate::i18n::text("ai.save-to-apply-existing-tasks-keep-their"), cx))
                    .child(
                        Button::new("ai-save-main")
                            .primary()
                            .small().label(if state["return_to_chat"] == true {
                                crate::i18n::text("ai.save-and-start-chatting")
                            } else {
                                crate::i18n::text("ai.save-changes")
                            })
                            .disabled(busy || profiles.is_empty())
                            .on_click(move |_, _, cx| {
                                command(
                                    json!({"action":"ai-save-main","model":model_input.read(cx).value()}),
                                )
                            }),
                    );
            }
            if screen == "main" {
                body = body.child(
                    div()
                        .flex()
                        .items_center()
                        .justify_between()
                        .child(section(crate::i18n::text("ai.integrated-agents")))
                        .child(
                            div()
                                .flex()
                                .gap_2()
                                .child(
                                    Button::new("ai-refresh")
                                        .ghost()
                                        .small()
                                        .label(if state["discovering"] == true {
                                            crate::i18n::text("ai.checking-53")
                                        } else {
                                            crate::i18n::text("ai.refresh-status")
                                        })
                                        .disabled(busy || state["discovering"] == true)
                                        .on_click(|_, _, _| {
                                            command(json!({"action":"agent-refresh"}))
                                        }),
                                )
                                .child(
                                    Button::new("ai-add")
                                        .small()
                                        .label(crate::i18n::text("ai.connect-agent"))
                                        .disabled(busy)
                                        .on_click(|_, _, _| {
                                            command(json!({"action":"ai-screen","screen":"add"}))
                                        }),
                                ),
                        ),
                );
                if profiles.is_empty() {
                    body = body.child(hint(
                        crate::i18n::text("ai.no-agents-connected-yet-choose-an-ai"),
                        cx,
                    ));
                }
            }
            let mut integrated = card(cx).p_0();
            for (i, p) in profiles.iter().enumerate() {
                let id = p["id"].as_str().unwrap_or("").to_owned();
                let status = agent_status(state, p["engine"].as_str().unwrap_or(""));
                integrated = integrated.child(
                    div()
                        .px_4()
                        .py_3()
                        .flex()
                        .items_center()
                        .justify_between()
                        .gap_4()
                        .when(i > 0, |row| {
                            row.border_t_1().border_color(cx.theme().border)
                        })
                        .child(brand_icon(profile_brand(state, p)).size(px(20.)))
                        .child(
                            div()
                                .flex_1()
                                .min_w_0()
                                .flex()
                                .flex_col()
                                .gap_1()
                                .child(p["name"].as_str().unwrap_or("Agent").to_owned())
                                .child(hint(
                                    format!("{} · {}", status, p["model"].as_str().unwrap_or("")),
                                    cx,
                                )),
                        )
                        .when(active["source_profile_id"] == id, |row| {
                            row.child(
                                div()
                                    .px_2()
                                    .py_1()
                                    .rounded_md()
                                    .bg(cx.theme().secondary)
                                    .text_sm()
                                    .child(crate::i18n::text("ai.main-agent")),
                            )
                        })
                        .child(
                            Button::new(("ai-manage", i))
                                .ghost()
                                .small()
                                .label(if status == crate::i18n::text("ai.sign-in-required") {
                                    crate::i18n::text("ai.sign-in-56")
                                } else {
                                    crate::i18n::text("ai.configure")
                                })
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"ai-manage","id":id}))
                                }),
                        ),
                );
            }
            if screen == "main" && !profiles.is_empty() {
                body = body.child(integrated);
            }
        }
        if screen != "main" {
            let kind = state["connection_kind"].as_str().unwrap_or("");
            let mut background = state.clone();
            background["ai_screen"] = json!("main");
            background["ai_busy"] = json!(true);
            background["design_underlay"] = json!(true);
            let main = self.render_ai(&background, cx);
            return div().relative().size_full().child(main).child(
                div().id("ai-config-overlay").absolute().inset_0().bg(cx.theme().background.opacity(0.84))
                    .on_key_down(move |event, _, cx| {
                        if event.keystroke.key == "escape" && !busy {
                            command(json!({"action":"ai-back"})); cx.stop_propagation();
                        }
                    })
                    .flex().items_center().justify_center().child(
                        sheet(card(cx).rounded(px(12.)).w(px(620.)).max_w(relative(0.95)).max_h(relative(0.92)).min_h_0()
                            .child(div().flex().items_center().gap_3()
                                .when(screen == "manage", |row| row.child(brand_icon(kind).size(px(28.))))
                                .child(div().flex_1().child(title(if screen == "add" { crate::i18n::text("ai.connect-agent-58") } else if screen == "main-edit" { crate::i18n::text("ai.main-agent") } else { brand_name(kind) })))
                                .when(screen == "manage" && state["connection_profile_id"].as_str().unwrap_or("").is_empty(), |row| row.child(
                                    Button::new("ai-back-choices").ghost().small().label(crate::i18n::text("usage-ui.back"))
                                        .disabled(busy).on_click(|_,_,_| command(json!({"action":"ai-screen","screen":"add"}))),
                                ))
                                .child(Button::new("ai-panel-close").ghost().small().label(crate::i18n::text("ai.close")).disabled(busy)
                                    .on_click(|_,_,_| command(json!({"action":"ai-back"}))))
                            )
                            .child(body)
                            .when(screen != "add" && !state["result"].as_str().unwrap_or("").is_empty(), |panel| panel.child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx))),
                            SharedString::from(format!("ai-panel-{}-{}", screen, state["secret_revision"].as_i64().unwrap_or(0))), cx),
                    ),
            );
        }
        page(cx).child(
            content()
                .child(title(crate::i18n::text("ui.ai-configuration")))
                .child(
                    segments(cx).children(
                        [
                            (0, crate::i18n::text("ui.general")),
                            (1, crate::i18n::text("ui.ai-configuration")),
                            (4, crate::i18n::text("ui.task-configuration")),
                            (3, crate::i18n::text("ui.work-environment")),
                        ]
                        .into_iter()
                        .map(|(i, label)| {
                            Button::new(("ai-tab", i as usize))
                                .ghost()
                                .small()
                                .label(label)
                                .toggled(i == 1)
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"section","value":i}))
                                })
                        }),
                    ),
                )
                .when(screen != "main", |el| {
                    el.child(
                        Button::new("ai-back")
                            .ghost()
                            .small()
                            .label(
                                if state["routing_owner"].as_str().is_some_and(|o| o != "main") {
                                    crate::i18n::text("ai.back-to-task-configuration")
                                } else {
                                    crate::i18n::text("ai.back-to-ai-configuration")
                                },
                            )
                            .disabled(busy)
                            .on_click(|_, _, _| command(json!({"action":"ai-back"}))),
                    )
                })
                .child(body)
                .when(!state["result"].as_str().unwrap_or("").is_empty(), |el| {
                    el.child(notice(
                        state["result"].as_str().unwrap_or("").to_owned(),
                        false,
                        cx,
                    ))
                })
                .when(state["return_to_chat"] == true, |el| {
                    el.child(
                        Button::new("ai-return-chat")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("ai.set-up-later-back-to-chat"))
                            .on_click(|_, _, _| command(json!({"action":"page","value":0}))),
                    )
                }),
        )
    }
}
