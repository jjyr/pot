use super::*;
use gpui_kit::component::Icon;
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

fn brand_icon(kind: &str) -> Icon {
    match kind {
        "codex" | "openai" => Icon::empty().data(include_bytes!("../../assets/agents/openai.svg")),
        "claude" | "anthropic" => {
            Icon::empty().data(include_bytes!("../../assets/agents/claude.svg"))
        }
        "gemini" => Icon::empty().data(include_bytes!("../../assets/agents/gemini.svg")),
        "agy" => Icon::new(IconName::SquareTerminal),
        _ => Icon::new(IconName::Globe),
    }
}

fn brand_name(kind: &str) -> &'static str {
    match kind {
        "codex" => "Codex",
        "claude" | "anthropic" => "Claude",
        "gemini" => "Gemini",
        "openai" => "OpenAI API",
        "agy" => "AGY",
        _ => "自定义 API",
    }
}

fn profile_brand<'a>(state: &'a Value, profile: &'a Value) -> &'a str {
    let engine = profile["engine"].as_str().unwrap_or("");
    if engine != "pi" {
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
    } else {
        "custom"
    }
}

pub(super) fn agent_status(state: &Value, engine: &str) -> &'static str {
    if engine == "pi" {
        return "已配置";
    }
    if state["discovering"] == true {
        return "检查中";
    }
    let agent = state["agents"]
        .as_array()
        .and_then(|a| a.iter().find(|a| a["id"] == engine));
    match agent {
        Some(a) if a["installation"] == "missing" => "未安装",
        Some(a) if a["status_check"] == "failed" => "暂时无法检测",
        Some(a) if a["authentication"] == "authenticated" => "已登录",
        Some(a) if a["authentication"] == "required" => "需要登录",
        _ => "状态未知",
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
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_4();
        if screen == "add" {
            body = body.child(hint("选择你想使用的 AI", cx));
            let mut choices = div().flex().flex_wrap().gap_3();
            for (i, id) in ["codex", "claude", "gemini", "openai", "custom", "agy"]
                .into_iter()
                .enumerate()
            {
                choices = choices.child(
                    div().w(relative(0.31)).child(
                        Button::new(("ai-add-kind", i))
                            .w_full()
                            .h(px(64.))
                            .label(brand_name(id))
                            .icon(brand_icon(id).size(px(24.)))
                            .disabled(busy)
                            .on_click(move |_, _, _| command(json!({"action":"ai-new","kind":id}))),
                    ),
                );
            }
            body = body.child(choices);
        } else if screen == "manage" {
            let native = matches!(
                state["connection_kind"].as_str(),
                Some("codex" | "claude" | "agy")
            );
            let kind = state["connection_kind"].as_str().unwrap_or("").to_owned();
            let mut form = Form::horizontal().label_width(px(90.));
            if (!native && self.api_advanced) || (native && self.commands_open) {
                form = form.child(
                    Field::new()
                        .label("名称")
                        .child(Input::new(&self.fields[23]).aria_label("Agent 名称")),
                );
            }
            if matches!(kind.as_str(), "claude" | "anthropic")
                && state["connection_profile_id"]
                    .as_str()
                    .unwrap_or("")
                    .is_empty()
            {
                form = form.child(
                    Field::new().label("连接方式").child(
                        div()
                            .flex()
                            .gap_2()
                            .child(
                                Button::new("ai-claude-login")
                                    .label("账号登录")
                                    .toggled(native)
                                    .disabled(busy)
                                    .on_click(|_, _, _| {
                                        command(json!({"action":"ai-new","kind":"claude"}))
                                    }),
                            )
                            .child(
                                Button::new("ai-claude-api")
                                    .label("API key")
                                    .toggled(!native)
                                    .disabled(busy)
                                    .on_click(|_, _, _| {
                                        command(json!({"action":"ai-new","kind":"anthropic"}))
                                    }),
                            ),
                    ),
                );
            }
            if native {
                let status = agent_status(state, &kind);
                let login_kind = kind.clone();
                form = form
                    .child(Field::new().label("连接状态").child(status))
                    .child(
                        Field::new()
                            .label("登录")
                            .description("在 Terminal 完成登录后，返回这里继续设置。")
                            .child(
                                Button::new("ai-login")
                                    .label(if status == "已登录" {
                                        "查看登录终端"
                                    } else {
                                        "前往 Terminal 登录"
                                    })
                                    .disabled(busy)
                                    .on_click(move |_, _, _| {
                                        command(json!({"action":"login-open","engine":login_kind}))
                                    }),
                            ),
                    );
                if status == "未安装" {
                    form = form.child(Field::new().label("安装工具").description("Pot 镜像预装支持的 CLI；可重试环境准备，或在 Terminal 自行修复缺失工具。")
                        .child(Button::new("ai-install").label("准备环境与工具").on_click(|_,_,_|command(json!({"action":"setup-stage","value":0})))));
                }
                form = form.child(
                    Field::new().label("检查状态").child(
                        Button::new("ai-check-native")
                            .label("重新检查")
                            .disabled(busy || state["discovering"] == true)
                            .on_click(|_, _, _| command(json!({"action":"agent-refresh"}))),
                    ),
                );
            } else {
                if kind == "custom" || self.api_advanced {
                    form = form.child(
                        Field::new()
                            .label("API 地址")
                            .description("填写 HTTPS 基础地址或完整请求地址，Pot 会补全必要路径。")
                            .child(Input::new(&self.fields[14]).aria_label("API 地址")),
                    );
                }
                if self.api_advanced
                    && matches!(
                        state["api_protocol"].as_str(),
                        Some("openai_chat" | "openai_responses" | "anthropic_messages" | "gemini")
                    )
                {
                    let mut formats = div().flex().gap_2();
                    for (i, (protocol, label)) in [
                        ("openai_chat", "Chat Completions"),
                        ("openai_responses", "Responses"),
                        ("anthropic_messages", "Messages"),
                        ("gemini", "Gemini"),
                    ]
                    .into_iter()
                    .enumerate()
                    {
                        formats = formats.child(
                            Button::new(("ai-protocol", i))
                                .ghost()
                                .label(label)
                                .toggled(state["api_protocol"] == protocol)
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"api-protocol","protocol":protocol}))
                                }),
                        );
                    }
                    form = form.child(Field::new().label("请求格式").child(formats));
                }
                form = form.child(
                    Field::new()
                        .label("API key")
                        .description(if state["has_api_key"] == true {
                            "已保存；同一地址留空可保留。"
                        } else {
                            "提供服务凭据，Pot 帮你完成配置。"
                        })
                        .child(Input::new(&self.fields[15]).aria_label("API key")),
                );
            }
            if let Some(models) = state["model_options"].as_array().filter(|m| !m.is_empty()) {
                let models: Vec<String> = models
                    .iter()
                    .filter_map(|m| m.as_str().map(str::to_owned))
                    .collect();
                let field = self.fields[16].clone();
                form = form.child(
                    Field::new().label("可用模型").child(
                        Button::new("ai-model-options")
                            .label("选择模型")
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
                    ),
                );
            }
            form = form.child(
                Field::new()
                    .label("模型")
                    .description(if native {
                        "填写账号支持的模型名称。"
                    } else {
                        "选择查询结果，或填写模型名称。"
                    })
                    .child(Input::new(&self.fields[16]).aria_label("连接模型")),
            );
            let fields = self.fields.clone();
            let check_fields = self.fields.clone();
            let mut actions = div().flex().flex_wrap().justify_end().gap_2();
            if !native {
                actions = actions.child(Button::new("ai-api-check").label(if busy {"正在处理…"} else {"检查连接"})
                    .disabled(busy).on_click(move |_, _, cx| command(json!({"action":"ai-api-check","endpoint":check_fields[14].read(cx).value(),"key":check_fields[15].read(cx).value()}))));
            }
            actions = actions.child(Button::new("ai-save-connection").primary().label("保存连接").disabled(busy)
                .on_click(move |_, _, cx| command(json!({"action":"ai-save-connection","name":fields[23].read(cx).value(),"model":fields[16].read(cx).value(),"endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value()}))));
            let mut details = div().flex().flex_col().gap_4();
            details = details.child(form);
            if !native {
                details =
                    details
                        .child(hint("检查可获取模型列表，也可以直接保存。", cx))
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
                        .child(
                            Button::new("ai-advanced")
                                .ghost()
                                .label("高级设置")
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    if native {
                                        this.settings.commands_open = !this.settings.commands_open;
                                    } else {
                                        this.settings.api_advanced = !this.settings.api_advanced;
                                    }
                                    cx.notify();
                                })),
                        )
                        .child(actions),
                ),
            );
            if native && self.commands_open {
                let fields = self.fields.clone();
                body=body.child(card(cx).child(hint("先保存连接后编辑调用命令；不更改聊天选择。",cx))
                    .child(Input::new(&self.fields[8]).aria_label("调用程序"))
                    .child(Input::new(&self.fields[9]).aria_label("额外参数 JSON"))
                    .child(Button::new("ai-save-invocation").label("保存调用命令").disabled(busy || state["connection_name"].is_null())
                        .on_click(move|_,_,cx|command(json!({"action":"save-invocation","executable":fields[8].read(cx).value(),"argv":fields[9].read(cx).value()})))));
            }
        } else {
            let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
            let draft = state["draft_profile"].as_str().unwrap_or("").to_owned();
            let selected = profiles.iter().find(|p| p["id"] == draft);
            let label = selected
                .and_then(|p| p["name"].as_str())
                .unwrap_or("请选择 Agent")
                .to_owned();
            let options = profiles.clone();
            let field = self.fields[22].clone();
            let mut form = Form::horizontal()
                .label_width(px(140.))
                .child(
                    Field::new().label("Agent").child(
                        Button::new("ai-main-picker")
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
                                        .checked(id == draft.to_owned())
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
                    Field::new()
                        .label("模型")
                        .description("填写所选 Agent 支持的模型标识；更改需保存才生效。")
                        .child(Input::new(&field).aria_label("聊天模型").disabled(busy)),
                );
            if let Some(selected) = selected {
                let engine = selected["engine"].as_str().unwrap_or("");
                let status = agent_status(state, engine);
                form = form.child(Field::new().label("连接状态").child(status));
                if engine != "pi" && status != "已登录" {
                    let id = selected["id"].as_str().unwrap_or("").to_owned();
                    form = form.child(
                        Field::new().label("完成连接").child(
                            Button::new("ai-complete")
                                .label("登录与模型设置")
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
                        .child(title("主 Agent"))
                        .child(hint(
                            "Pot 将使用此 Agent 进行主要对话，并默认用于新任务。",
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
                                    .size(px(24.)),
                                )
                                .child(
                                    div().flex_1().child(
                                        state["active_name"]
                                            .as_str()
                                            .filter(|_| active["model"].as_str().is_some())
                                            .unwrap_or("尚未配置")
                                            .to_owned(),
                                    ),
                                )
                                .child(hint(
                                    active["model"]
                                        .as_str()
                                        .unwrap_or("集成后选择 Agent 和模型"),
                                    cx,
                                ))
                                .child(
                                    Button::new("ai-edit-main")
                                        .label(if active["model"].as_str().is_some() {
                                            "更改"
                                        } else {
                                            "设置"
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
                    .child(hint("保存后生效，已有任务保持原配置。", cx))
                    .child(
                        Button::new("ai-save-main")
                            .primary()
                            .label(if state["return_to_chat"] == true {
                                "保存并开始聊天"
                            } else {
                                "保存更改"
                            })
                            .disabled(busy || profiles.is_empty())
                            .on_click(move |_, _, cx| {
                                command(
                                    json!({"action":"ai-save-main","model":field.read(cx).value()}),
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
                        .child(title("已集成 Agent"))
                        .child(
                            div()
                                .flex()
                                .gap_2()
                                .child(
                                    Button::new("ai-refresh")
                                        .ghost()
                                        .label(if state["discovering"] == true {
                                            "正在检查…"
                                        } else {
                                            "刷新状态"
                                        })
                                        .disabled(busy || state["discovering"] == true)
                                        .on_click(|_, _, _| {
                                            command(json!({"action":"agent-refresh"}))
                                        }),
                                )
                                .child(
                                    Button::new("ai-add")
                                        .label("＋ 集成 Agent")
                                        .disabled(busy)
                                        .on_click(|_, _, _| {
                                            command(json!({"action":"ai-screen","screen":"add"}))
                                        }),
                                ),
                        ),
                );
                if profiles.is_empty() {
                    body = body.child(hint("尚未集成 Agent。选择你想使用的 AI 开始配置。", cx));
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
                        .child(brand_icon(profile_brand(state, p)).size(px(24.)))
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
                                    .child("主 Agent"),
                            )
                        })
                        .child(
                            Button::new(("ai-manage", i))
                                .ghost()
                                .label(if status == "需要登录" {
                                    "去登录"
                                } else {
                                    "配置"
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
            let main = self.render_ai(&background, cx);
            return div().relative().size_full().child(main).child(
                div().id("ai-config-overlay").absolute().inset_0().bg(cx.theme().background.opacity(0.84))
                    .on_key_down(move |event, _, cx| {
                        if event.keystroke.key == "escape" && !busy {
                            command(json!({"action":"ai-back"})); cx.stop_propagation();
                        }
                    })
                    .flex().items_center().justify_center().child(
                        card(cx).w(px(620.)).max_w(relative(0.95)).max_h(relative(0.92)).min_h_0()
                            .child(div().flex().items_center().gap_3()
                                .when(screen == "manage", |row| row.child(brand_icon(kind).size(px(28.))))
                                .child(div().flex_1().child(title(if screen == "add" { "集成 Agent" } else if screen == "main-edit" { "主 Agent" } else { brand_name(kind) })))
                                .when(screen == "manage" && state["connection_profile_id"].as_str().unwrap_or("").is_empty(), |row| row.child(
                                    Button::new("ai-back-choices").ghost().label("返回")
                                        .disabled(busy).on_click(|_,_,_| command(json!({"action":"ai-screen","screen":"add"}))),
                                ))
                                .child(Button::new("ai-panel-close").ghost().label("关闭").disabled(busy)
                                    .on_click(|_,_,_| command(json!({"action":"ai-back"}))))
                            )
                            .child(body)
                            .when(screen != "add" && !state["result"].as_str().unwrap_or("").is_empty(), |panel| panel.child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx))),
                    ),
            );
        }
        page(cx).child(
            content()
                .child(title("设置"))
                .child(
                    div().flex().gap_2().children(
                        [
                            (0, "通用"),
                            (1, "AI 配置"),
                            (4, "任务配置"),
                            (3, "工作环境"),
                        ]
                        .into_iter()
                        .map(|(i, label)| {
                            Button::new(("ai-tab", i as usize))
                                .ghost()
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
                            .label(
                                if state["routing_owner"].as_str().is_some_and(|o| o != "main") {
                                    "← 返回任务配置"
                                } else {
                                    "← 返回 AI 配置"
                                },
                            )
                            .disabled(busy)
                            .on_click(|_, _, _| command(json!({"action":"ai-back"}))),
                    )
                })
                .child(body)
                .child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx))
                .when(state["return_to_chat"] == true, |el| {
                    el.child(
                        Button::new("ai-return-chat")
                            .ghost()
                            .label("稍后设置 · 返回聊天")
                            .on_click(|_, _, _| command(json!({"action":"page","value":0}))),
                    )
                }),
        )
    }
}
