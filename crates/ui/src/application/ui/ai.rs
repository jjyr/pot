use super::*;
use crate::pages::{Control, FieldHelp};
use gpui_kit::component::IconName;
use gpui_kit::component::form::Field;
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
    cx: &App,
) -> Button {
    Button::new(id)
        .w_full()
        .h(px(52.))
        .control(cx)
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

fn connection_summary(
    icon: gpui_kit::component::Icon,
    name: String,
    model: String,
    cx: &App,
) -> Div {
    div()
        .flex_1()
        .min_w(px(140.))
        .flex()
        .items_center()
        .gap_3()
        .when(pot_ui::design::mobile(cx), |el| el.w_full())
        .child(icon.size(px(20.)).flex_shrink_0())
        .child(
            div()
                .flex_1()
                .min_w_0()
                .flex()
                .flex_col()
                .gap_1()
                .child(
                    div()
                        .w_full()
                        .min_w_0()
                        .overflow_hidden()
                        .text_ellipsis()
                        .child(name),
                )
                .child(hint(model, cx).w_full().overflow_hidden().text_ellipsis()),
        )
}

impl Settings {
    fn token_limit_field(
        &self,
        index: usize,
        label: &'static str,
        presets: &[(&'static str, u64)],
        busy: bool,
        cx: &App,
    ) -> Field {
        let mut buttons = div().w_full().min_w_0().flex().flex_wrap().gap_2();
        for &(name, value) in presets {
            let input = self.fields[index].clone();
            buttons = buttons.child(
                Button::new(SharedString::from(format!(
                    "ai-token-preset-{index}-{value}"
                )))
                .control(cx)
                .rounded_full()
                .label(name)
                .disabled(busy)
                .on_click(move |_, window, cx| {
                    let value = value.to_string();
                    input.update(cx, |input, cx| input.set_value(value.clone(), window, cx));
                    command(json!({
                        "action": if index == 24 { "ai-context-input" } else { "ai-output-input" },
                        "value": value
                    }));
                }),
            );
        }
        field(label).child(
            div()
                .w_full()
                .flex()
                .flex_col()
                .min_w_0()
                .gap_2()
                .child(
                    Input::new(&self.fields[index])
                        .control(cx)
                        .aria_label(label),
                )
                .child(buttons),
        )
    }
    pub(super) fn render_ai(
        &self,
        state: &Value,
        location: Option<Div>,
        cx: &mut Context<Shell>,
    ) -> Div {
        let busy = state["ai_busy"] == true;
        let screen = state["ai_screen"].as_str().unwrap_or("main");
        let column_width = if cx.global::<pot_ui::design::Viewport>().0 < px(500.) {
            1.
        } else if cx.global::<pot_ui::design::Viewport>().0 < px(900.) {
            0.48
        } else {
            0.31
        };
        let mut secondary = Vec::new();
        let mut primary = None;
        if screen != "main" {
            secondary.push(
                Button::new("ai-cancel")
                    .ghost()
                    .label(crate::application::i18n::text("schedules-ui.cancel"))
                    .disabled(busy)
                    .on_click(|_, _, _| command(json!({"action":"ai-back"}))),
            );
        }
        let mut body = div()
            .id(SharedString::from(format!(
                "ai-scroll-{}-{}",
                screen,
                state["secret_revision"].as_i64().unwrap_or(0)
            )))
            .flex_1()
            .min_h_0()
            .min_w_0()
            .track_scroll(&self.scroll)
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
                    .control(cx)
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
                    div().w(relative(column_width)).child(
                        provider_choice(
                            ("ai-add-kind", i),
                            provider_name(state, &id),
                            provider_icon(state, &id),
                            busy,
                            cx,
                        )
                        .on_click(move |_, _, _| command(json!({"action":"ai-new","kind":id}))),
                    ),
                );
            }
            if query.is_empty() || "custom api 自定义".contains(&query) {
                choices = choices.child(
                    div().w(relative(column_width)).child(
                        provider_choice(
                            "ai-add-custom",
                            crate::application::i18n::text("ai.custom-api").to_owned(),
                            brand_icon("custom"),
                            busy,
                            cx,
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
                        .control(cx)
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
                            div().w(relative(column_width)).child(
                                provider_choice(
                                    ("ai-more-kind", i),
                                    provider_name(state, &id),
                                    provider_icon(state, &id),
                                    busy,
                                    cx,
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
            let mut form = super::form(cx)
                .child(
                    field(crate::application::i18n::text("schedules-ui.name")).child(
                        Input::new(&self.fields[23])
                            .control(cx)
                            .aria_label(crate::application::i18n::text("ai.agent-name")),
                    ),
                )
                .child(
                    field(crate::application::i18n::text("ai.api-url")).child(
                        Input::new(&self.fields[14])
                            .control(cx)
                            .aria_label(crate::application::i18n::text("ai.api-url")),
                    ),
                );
            {
                form = form.child(
                    field("API key")
                        .help(if state["has_api_key"] == true {
                            crate::application::i18n::text("ai.saved-leave-blank-to-keep-the-key")
                        } else {
                            crate::application::i18n::text(
                                "ai.provide-your-service-credentials-and-pot-will",
                            )
                        })
                        .child(
                            Input::new(&self.fields[15])
                                .control(cx)
                                .aria_label("API key"),
                        ),
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
                            .control(cx)
                            .label(crate::application::i18n::text("ai.get-api-key"))
                            .on_click(move |_, _, cx| cx.open_url(url)),
                    ),
                );
            }
            {
                let mut model_field = div()
                    .flex()
                    .flex_wrap()
                    .min_w_0()
                    .items_center()
                    .gap_2()
                    .child(
                        div().flex_1().min_w_0().child(
                            Input::new(&self.fields[16])
                                .control(cx)
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
                            .control(cx)
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
                form = form.child(
                    field(crate::application::i18n::text("ai.default-model")).child(model_field),
                );
            }
            if matches!(
                state["api_protocol"].as_str(),
                Some("openai_chat" | "openai_responses" | "anthropic_messages" | "gemini")
            ) {
                let mut formats = div()
                    .w_full()
                    .min_w_0()
                    .flex()
                    .flex_wrap()
                    .gap_2()
                    .when(pot_ui::design::mobile(cx), |el| el.flex_col());
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
                            .when(pot_ui::design::mobile(cx), |b| b.w_full().justify_start())
                            .ghost()
                            .control(cx)
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
            form = form
                .child(self.token_limit_field(
                    24,
                    crate::application::i18n::text("ai.context-window"),
                    &[
                        ("128K", 128_000),
                        ("200K", 200_000),
                        ("256K", 256_000),
                        ("400K", 400_000),
                        ("512K", 512_000),
                        ("1M", 1_000_000),
                    ],
                    busy,
                    cx,
                ))
                .child(self.token_limit_field(
                    25,
                    crate::application::i18n::text("ai.max-output-tokens"),
                    &[
                        ("8K", 8_000),
                        ("16K", 16_000),
                        ("32K", 32_000),
                        ("64K", 64_000),
                        ("128K", 128_000),
                    ],
                    busy,
                    cx,
                ))
                .child(
                    field(crate::application::i18n::text("ai.reasoning-effort")).child(
                        Input::new(&self.fields[27])
                            .control(cx)
                            .aria_label(crate::application::i18n::text("ai.reasoning-effort")),
                    ),
                );
            let fields = self.fields.clone();
            let check_fields = self.fields.clone();

            {
                secondary.push(Button::new("ai-api-check").control(cx).label(crate::application::i18n::text("ai.check-connection"))
                    .disabled(busy).on_click(move |_, _, cx| command(json!({"action":"ai-api-check","endpoint":check_fields[14].read(cx).value(),"key":check_fields[15].read(cx).value()}))));
            }
            primary = Some(Button::new("ai-save-connection").primary().control(cx).label(crate::application::i18n::text("editor.save")).disabled(busy)
                .on_click(move |_, _, cx| command(json!({"action":"ai-save-connection","name":fields[23].read(cx).value(),"model":fields[16].read(cx).value(),"endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value(),"context":fields[24].read(cx).value(),"output":fields[25].read(cx).value(),"effort":fields[27].read(cx).value()}))));
            let mut details = div().flex().flex_col().gap_4();
            details = details.child(form);
            if let Some(message) = state["check_message"].as_str().filter(|s| !s.is_empty()) {
                details = details.child(hint(message.to_owned(), cx));
            }
            body = body.child(details.flex_shrink_0().min_w_0());
        } else {
            let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
            let selected = state["default_provider_id"]
                .as_str()
                .unwrap_or("")
                .to_owned();
            let label = profiles
                .iter()
                .find(|p| p["id"] == selected)
                .and_then(|p| p["name"].as_str())
                .unwrap_or(crate::application::i18n::text("ai.not-configured"))
                .to_owned();
            let choices = profiles.clone();
            body=body.child(super::form(cx).child(field(crate::application::i18n::text("ai.default-provider")).child(
                Button::new("ai-default-provider").control(cx).label(label).dropdown_caret(true).disabled(busy || profiles.is_empty())
                    .dropdown_menu(move|menu,_,_|choices.iter().fold(menu,|menu,p| {
                        let id=p["id"].as_str().unwrap_or("").to_owned();
                        menu.item(PopupMenuItem::new(p["name"].as_str().unwrap_or("").to_owned()).checked(id==selected)
                            .on_click(move|_,_,_|command(json!({"action":"ai-default-provider","id":id}))))
                    }))
            ))).child(div().flex().items_center().justify_between().gap_3()
                .child(section(crate::application::i18n::text("ai.integrated-agents")))
                .child(Button::new("ai-add").control(cx).icon(IconName::Plus).label(crate::application::i18n::text("ai.connect-agent")).disabled(busy)
                    .on_click(|_,_,_|command(json!({"action":"ai-screen","screen":"add"})))));
            if profiles.is_empty() {
                body = body.child(hint(
                    crate::application::i18n::text("ai.no-agents-connected-yet-choose-an-ai"),
                    cx,
                ));
            }
            for (index, p) in profiles.iter().enumerate() {
                let id = p["id"].as_str().unwrap_or("").to_owned();
                body = body.child(
                    card(cx)
                        .flex()
                        .flex_row()
                        .flex_wrap()
                        .items_center()
                        .gap_3()
                        .child(connection_summary(
                            provider_icon(state, profile_brand(state, p)),
                            p["name"].as_str().unwrap_or("").to_owned(),
                            p["model"].as_str().unwrap_or("").to_owned(),
                            cx,
                        ))
                        .child(
                            Button::new(("ai-manage", index))
                                .ghost()
                                .control(cx)
                                .label(crate::application::i18n::text("ai.configure"))
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"ai-manage","id":id}))
                                }),
                        ),
                );
            }
        }

        if screen != "main" {
            if screen != "add" && !state["result"].as_str().unwrap_or("").is_empty() {
                body = body.child(hint(
                    crate::application::i18n::message(state["result"].as_str().unwrap_or("")),
                    cx,
                ));
            }
            if state["retry"] == true {
                secondary.push(
                    Button::new("ai-editor-retry")
                        .control(cx)
                        .label(crate::application::i18n::text("ui.retry"))
                        .on_click(|_, _, _| command(json!({"action":"ai-retry"}))),
                );
            }
            let body = pot_ui::pages::reveal_inputs(
                body,
                self.fields.clone(),
                self.scroll.clone(),
                &self.reveal,
            );
            return pot_ui::pages::editor_page(
                body,
                pot_ui::pages::editor_actions(secondary, primary, cx),
                cx,
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
                            .control(cx)
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
                        crate::application::i18n::message(state["result"].as_str().unwrap_or("")),
                        false,
                        cx,
                    ))
                })
                .when(state["return_to_chat"] == true, |el| {
                    el.child(
                        Button::new("ai-return-chat")
                            .ghost()
                            .control(cx)
                            .label(crate::application::i18n::text(
                                "ai.set-up-later-back-to-chat",
                            ))
                            .on_click(|_, _, _| command(json!({"action":"page","value":0}))),
                    )
                }),
        )
    }
}
