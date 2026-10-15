use super::*;
use gpui_kit::component::IconName;
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

fn directory_provider<'a>(state: &'a Value, id: &str) -> Option<&'a Value> {
    state["directory"]
        .as_array()?
        .iter()
        .find(|p| p["id"] == id)
}
fn provider_name(state: &Value, id: &str) -> String {
    directory_provider(state, id)
        .and_then(|p| p["name"].as_str())
        .unwrap_or(crate::application::i18n::text("ai.custom-api"))
        .to_owned()
}
fn provider_icon(state: &Value, id: &str) -> gpui_kit::component::Icon {
    if let Some(logo) = directory_provider(state, id).and_then(|p| p["logo"].as_str()) {
        gpui_kit::component::Icon::empty().data(logo.as_bytes())
    } else {
        brand_icon(if id == "google" { "gemini" } else { id })
    }
}
fn profile_brand<'a>(state: &'a Value, profile: &'a Value) -> &'a str {
    state["connections"]
        .as_array()
        .into_iter()
        .flatten()
        .find(|p| p["profile_id"] == profile["id"])
        .and_then(|p| p["model_settings"]["catalog_provider"].as_str())
        .unwrap_or("custom")
}

fn provider_choice(
    id: impl Into<ElementId>,
    name: String,
    icon: gpui_kit::component::Icon,
    busy: bool,
) -> Button {
    Button::new(id)
        .w_full()
        .h(px(52.))
        .small()
        .accessibility_label(name.clone())
        .child(
            div()
                .flex()
                .items_center()
                .gap_3()
                .min_w_0()
                .child(icon.size(px(28.)).flex_shrink_0())
                .child(div().min_w_0().text_ellipsis().child(name)),
        )
        .disabled(busy)
}

impl Settings {
    pub(super) fn render_ai(
        &self,
        state: &Value,
        location: Option<Div>,
        cx: &mut Context<Shell>,
    ) -> Div {
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
                crate::application::i18n::text("ai.choose-the-ai-you-want-to-use"),
                cx,
            ));
            body = body.child(
                Input::new(&self.fields[26])
                    .aria_label(crate::application::i18n::text("ai.search-providers")),
            );
            let query = self.fields[26].read(cx).value().trim().to_lowercase();
            let providers = super::super::settings_state::provider_choices(state, &query);
            let mut choices = div().flex().flex_wrap().gap_3();
            for (i, provider) in providers
                .iter()
                .filter(|p| !query.is_empty() || p["common"] == true)
                .enumerate()
            {
                let id = provider["id"].as_str().unwrap_or("custom").to_owned();
                choices = choices.child(
                    div().w(relative(0.31)).child(
                        provider_choice(
                            ("ai-add-kind", i),
                            provider_name(state, &id),
                            provider_icon(state, &id),
                            busy,
                        )
                        .on_click(move |_, _, _| command(json!({"action":"ai-new","kind":id}))),
                    ),
                );
            }
            if query.is_empty() || "custom api 自定义".contains(&query) {
                choices = choices.child(
                    div().w(relative(0.31)).child(
                        provider_choice(
                            "ai-add-custom",
                            crate::application::i18n::text("ai.custom-api").to_owned(),
                            brand_icon("custom"),
                            busy,
                        )
                        .on_click(|_, _, _| command(json!({"action":"ai-new","kind":"custom"}))),
                    ),
                );
            }
            body = body.child(choices);
            if query.is_empty() {
                body = body.child(
                    Button::new("ai-more-providers")
                        .ghost()
                        .small()
                        .label(crate::application::i18n::text(
                            if state["more_providers"] == true {
                                "ai.fewer-providers"
                            } else {
                                "ai.more-providers"
                            },
                        ))
                        .on_click(|_, _, _| command(json!({"action":"ai-more-providers"}))),
                );
                if state["more_providers"] == true {
                    let mut more = div().flex().flex_wrap().gap_3();
                    for (i, provider) in
                        providers.iter().filter(|p| p["common"] != true).enumerate()
                    {
                        let id = provider["id"].as_str().unwrap_or("").to_owned();
                        more = more.child(
                            div().w(relative(0.31)).child(
                                provider_choice(
                                    ("ai-more-kind", i),
                                    provider_name(state, &id),
                                    provider_icon(state, &id),
                                    busy,
                                )
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"ai-new","kind":id}))
                                }),
                            ),
                        );
                    }
                    body = body.child(more);
                }
            }
        } else if screen == "manage" {
            let kind = state["connection_kind"].as_str().unwrap_or("").to_owned();
            let mut form = super::form(cx);
            if kind == "custom" && !self.api_advanced {
                form = form.child(
                    field(crate::application::i18n::text("ai.api-url")).child(
                        Input::new(&self.fields[14])
                            .aria_label(crate::application::i18n::text("ai.api-url")),
                    ),
                );
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
                        Button::new(("ai-custom-protocol", i))
                            .ghost()
                            .small()
                            .label(label)
                            .toggled(state["api_protocol"] == protocol)
                            .on_click(move |_, _, _| {
                                command(json!({"action":"api-protocol","protocol":protocol}))
                            }),
                    );
                }
                form = form.child(
                    field(crate::application::i18n::text("ai.request-format")).child(formats),
                );
            }
            {
                form = form.child(
                    field("API key")
                        .description(if state["has_api_key"] == true {
                            crate::application::i18n::text("ai.saved-leave-blank-to-keep-the-key")
                        } else {
                            crate::application::i18n::text(
                                "ai.provide-your-service-credentials-and-pot-will",
                            )
                        })
                        .child(Input::new(&self.fields[15]).aria_label("API key")),
                );
            }
            let key_url = match kind.as_str() {
                "anthropic" => Some("https://platform.claude.com/settings/keys"),
                "google" => Some("https://aistudio.google.com/api-keys"),
                "deepseek" => Some("https://platform.deepseek.com/api_keys"),
                "opencode-go" | "opencode" => Some("https://opencode.ai/auth"),
                "openai" => Some("https://platform.openai.com/api-keys"),
                _ => None,
            };
            if let Some(url) = key_url {
                form = form.child(
                    field("").child(
                        Button::new("ai-get-key")
                            .ghost()
                            .small()
                            .label(crate::application::i18n::text("ai.get-api-key"))
                            .on_click(move |_, _, cx| cx.open_url(url)),
                    ),
                );
            }
            {
                let mut model_field = div().flex().items_center().gap_2().child(
                    div().flex_1().min_w_0().child(
                        Input::new(&self.fields[16])
                            .aria_label(crate::application::i18n::text("ai.connection-model")),
                    ),
                );
                let mut models = std::collections::BTreeMap::<String, String>::new();
                if let Some(provider) = directory_provider(state, &kind) {
                    for model in provider["models"].as_array().into_iter().flatten() {
                        if let Some(id) = model["id"].as_str() {
                            models.insert(
                                id.to_owned(),
                                model["name"].as_str().unwrap_or(id).to_owned(),
                            );
                        }
                    }
                }
                for model in state["model_options"].as_array().into_iter().flatten() {
                    if let Some(id) = model.as_str() {
                        models.entry(id.to_owned()).or_insert_with(|| id.to_owned());
                    }
                }
                let current = self.fields[16].read(cx).value().to_string();
                let query = if models.contains_key(&current) {
                    String::new()
                } else {
                    current.to_lowercase()
                };
                let models: Vec<_> = models
                    .into_iter()
                    .filter(|(id, name)| {
                        query.is_empty()
                            || id.to_lowercase().contains(&query)
                            || name.to_lowercase().contains(&query)
                    })
                    .collect();
                if !models.is_empty() {
                    model_field = model_field.child(
                        Button::new("ai-model-options")
                            .small()
                            .label(crate::application::i18n::text("ai.choose"))
                            .dropdown_caret(true)
                            .dropdown_menu(move |menu, _, _| {
                                models.iter().fold(menu, |menu, (id, name)| {
                                    let id = id.clone();
                                    menu.item(PopupMenuItem::new(name.clone()).on_click(
                                        move |_, _, _| {
                                            command(json!({"action":"ai-model","model":id}))
                                        },
                                    ))
                                })
                            }),
                    );
                }
                form = form
                    .child(field(crate::application::i18n::text("ai.model")).child(model_field));
            }
            form = form.child(
                field("").child(
                    Button::new("ai-advanced")
                        .ghost()
                        .small()
                        .label(crate::application::i18n::text("ai.advanced-settings"))
                        .on_click(cx.listener(|this, _, _, cx| {
                            this.settings.api_advanced = !this.settings.api_advanced;
                            cx.notify();
                        })),
                ),
            );
            if self.api_advanced {
                form = form.child(
                    field(crate::application::i18n::text("schedules-ui.name")).child(
                        Input::new(&self.fields[23])
                            .aria_label(crate::application::i18n::text("ai.agent-name")),
                    ),
                );
            }
            if self.api_advanced {
                form = form.child(
                    field(crate::application::i18n::text("ai.api-url")).child(
                        Input::new(&self.fields[14])
                            .aria_label(crate::application::i18n::text("ai.api-url")),
                    ),
                );
            }
            if self.api_advanced
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
                form = form.child(
                    field(crate::application::i18n::text("ai.request-format")).child(formats),
                );
            }
            if self.api_advanced {
                form = form
                    .child(
                        field(crate::application::i18n::text("ai.context-window")).child(
                            Input::new(&self.fields[24])
                                .aria_label(crate::application::i18n::text("ai.context-window")),
                        ),
                    )
                    .child(
                        field(crate::application::i18n::text("ai.max-output-tokens"))
                            .child(Input::new(&self.fields[25]).aria_label(
                                crate::application::i18n::text("ai.max-output-tokens"),
                            )),
                    );
            }
            let fields = self.fields.clone();
            let check_fields = self.fields.clone();
            let mut actions = div().flex().flex_wrap().justify_end().gap_2();
            {
                actions = actions.child(Button::new("ai-api-check").small().label(crate::application::i18n::text("ai.check-connection"))
                    .disabled(busy).on_click(move |_, _, cx| command(json!({"action":"ai-api-check","endpoint":check_fields[14].read(cx).value(),"key":check_fields[15].read(cx).value()}))));
            }
            actions = actions.child(Button::new("ai-save-connection").primary().small().label(if state["active_spec"]["model"].as_str().is_none() && state["routing_owner"] == "main" {
                    if state["return_to_chat"] == true { crate::application::i18n::text("ai.save-and-start-chatting") } else { crate::application::i18n::text("ai.save-and-set-as-main-agent") }
                } else { crate::application::i18n::text("ai.save-connection") }).disabled(busy)
                .on_click(move |_, _, cx| command(json!({"action":"ai-save-connection","name":fields[23].read(cx).value(),"model":fields[16].read(cx).value(),"endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value(),"context":fields[24].read(cx).value(),"output":fields[25].read(cx).value()}))));
            let mut details = div().flex().flex_col().gap_4();
            details = details.child(form);
            if let Some(message) = state["check_message"].as_str().filter(|s| !s.is_empty()) {
                details = details.child(hint(message.to_owned(), cx));
            }
            body = body.child(
                details.child(
                    div()
                        .flex()
                        .items_center()
                        .justify_between()
                        .gap_2()
                        .child(actions),
                ),
            );
        } else {
            let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
            let draft = state["draft_profile"].as_str().unwrap_or("").to_owned();
            let selected = profiles.iter().find(|p| p["id"] == draft);
            let label = selected
                .and_then(|p| p["name"].as_str())
                .unwrap_or(crate::application::i18n::text("ai.choose-an-agent"))
                .to_owned();
            let options = profiles.clone();
            let model_input = self.fields[22].clone();
            let form = super::form(cx)
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
                    field(crate::application::i18n::text("ai.model"))
                        .description(crate::application::i18n::text(
                            "ai.enter-a-model-supported-by-this-agent",
                        ))
                        .child(
                            Input::new(&model_input)
                                .aria_label(crate::application::i18n::text("ai.chat-model"))
                                .disabled(busy),
                        ),
                );
            let no_profiles = profiles.is_empty();
            let active = &state["active_spec"];
            if screen == "main" {
                let active_profile = profiles
                    .iter()
                    .find(|p| p["id"] == active["source_profile_id"]);
                body = body.child(
                    card(cx)
                        .child(section(crate::application::i18n::text("ai.main-agent")))
                        .child(hint(
                            crate::application::i18n::text("ai.pot-will-use-this-agent-for-the"),
                            cx,
                        ))
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap_3()
                                .child(
                                    provider_icon(state,
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
                                            .unwrap_or(crate::application::i18n::text("ai.not-configured"))
                                            .to_owned(),
                                    ),
                                )
                                .child(hint(
                                    active["model"].as_str().unwrap_or(crate::application::i18n::text(
                                        "ai.connect-an-agent-then-choose-a-model",
                                    )),
                                    cx,
                                ))
                                .child(
                                    Button::new("ai-edit-main")
                                        .small()
                                        .label(if active["model"].as_str().is_some() {
                                            crate::application::i18n::text("ai.change")
                                        } else {
                                            crate::application::i18n::text("ai.connect-agent")
                                        })
                                        .when(active["model"].as_str().is_none(), |b| {
                                            b.icon(IconName::Plus)
                                        })
                                        .when(no_profiles, |b| b.primary())
                                        .disabled(busy)
                                        .on_click(move |_, _, _| {
                                            command(
                                                json!({"action":"ai-screen","screen":if no_profiles { "add" } else { "main-edit" }}),
                                            )
                                        }),
                                ),
                        ),
                );
            } else {
                body = body
                    .child(form)
                    .child(hint(crate::application::i18n::text("ai.save-to-apply-existing-tasks-keep-their"), cx))
                    .child(
                        Button::new("ai-save-main")
                            .primary()
                            .small().label(if state["return_to_chat"] == true {
                                crate::application::i18n::text("ai.save-and-start-chatting")
                            } else {
                                crate::application::i18n::text("ai.save-changes")
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
                        .child(section(crate::application::i18n::text(
                            "ai.integrated-agents",
                        )))
                        .child(
                            div().flex().gap_2().child(
                                Button::new("ai-add")
                                    .small()
                                    .icon(IconName::Plus)
                                    .label(crate::application::i18n::text("ai.connect-agent"))
                                    .disabled(busy)
                                    .on_click(|_, _, _| {
                                        command(json!({"action":"ai-screen","screen":"add"}))
                                    }),
                            ),
                        ),
                );
                if profiles.is_empty() {
                    body = body.child(hint(
                        crate::application::i18n::text("ai.no-agents-connected-yet-choose-an-ai"),
                        cx,
                    ));
                }
            }
            let mut integrated = card(cx).p_0();
            for (i, p) in profiles.iter().enumerate() {
                let id = p["id"].as_str().unwrap_or("").to_owned();
                let status = crate::application::i18n::text("ai.configured");
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
                        .child(provider_icon(state, profile_brand(state, p)).size(px(20.)))
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
                                    .child(crate::application::i18n::text("ai.main-agent")),
                            )
                        })
                        .child(
                            Button::new(("ai-manage", i))
                                .ghost()
                                .small()
                                .label(
                                    if status
                                        == crate::application::i18n::text("ai.sign-in-required")
                                    {
                                        crate::application::i18n::text("ai.sign-in-56")
                                    } else {
                                        crate::application::i18n::text("ai.configure")
                                    },
                                )
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
            let main = self.render_ai(&background, location, cx);
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
                                .when(screen == "manage", |row| row.child(provider_icon(state,kind).size(px(28.))))
                                .child(div().flex_1().child(title(if screen == "add" { crate::application::i18n::text("ai.connect-agent-58").to_owned() } else if screen == "main-edit" { crate::application::i18n::text("ai.main-agent").to_owned() } else { provider_name(state,kind) })))
                                .when(screen == "manage" && state["connection_profile_id"].as_str().unwrap_or("").is_empty(), |row| row.child(
                                    Button::new("ai-back-choices").ghost().small().label(crate::application::i18n::text("usage-ui.back"))
                                        .disabled(busy).on_click(|_,_,_| command(json!({"action":"ai-screen","screen":"add"}))),
                                ))
                                .child(Button::new("ai-panel-close").ghost().small().label(crate::application::i18n::text("ai.close")).disabled(busy)
                                    .on_click(|_,_,_| command(json!({"action":"ai-back"}))))
                            )
                            .child(body)
                            .when(screen != "add" && !state["result"].as_str().unwrap_or("").is_empty(), |panel| panel.child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx))),
                            SharedString::from(format!("ai-panel-{}-{}", screen, state["secret_revision"].as_i64().unwrap_or(0))), cx),
                    ),
            );
        }
        page(cx).child(
            content(cx)
                .child(super::settings_header(1, busy, location, cx))
                .child(hint(
                    crate::application::i18n::text("ui.connect-an-agent-and-choose-the-model"),
                    cx,
                ))
                .when(screen != "main", |el| {
                    el.child(
                        Button::new("ai-back")
                            .ghost()
                            .small()
                            .label(
                                if state["routing_owner"].as_str().is_some_and(|o| o != "main") {
                                    crate::application::i18n::text("ai.back-to-task-configuration")
                                } else {
                                    crate::application::i18n::text("ai.back-to-ai-configuration")
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
                            .label(crate::application::i18n::text(
                                "ai.set-up-later-back-to-chat",
                            ))
                            .on_click(|_, _, _| command(json!({"action":"page","value":0}))),
                    )
                }),
        )
    }
}
