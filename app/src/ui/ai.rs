use super::*;
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

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
            body = body
                .child(title("添加 Agent"))
                .child(hint("选择你想使用的 Agent，Pot 会引导你完成相应连接。", cx));
            for (i, (id, name, description)) in [
                ("codex", "Codex", "在专用 Terminal 完成官方登录。"),
                ("claude", "Claude", "在专用 Terminal 完成官方登录。"),
                ("agy", "AGY", "按原版 CLI 指引登录；状态检测暂不可用。"),
                (
                    "deepseek",
                    "DeepSeek",
                    "提供 API key，Pot 自动填写服务配置。",
                ),
                ("openai", "OpenAI", "提供 API key，Pot 自动填写服务配置。"),
                (
                    "anthropic",
                    "Anthropic",
                    "提供 API key，Pot 自动填写服务配置。",
                ),
                ("gemini", "Gemini", "提供 API key，Pot 自动填写服务配置。"),
                ("custom", "自定义 Agent", "连接其他支持的模型服务。"),
            ]
            .into_iter()
            .enumerate()
            {
                body = body.child(
                    card(cx)
                        .child(
                            Button::new(("ai-add-kind", i))
                                .label(name)
                                .disabled(busy)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"ai-new","kind":id}))
                                }),
                        )
                        .child(hint(description, cx)),
                );
            }
        } else if screen == "manage" {
            let native = matches!(
                state["connection_kind"].as_str(),
                Some("codex" | "claude" | "agy")
            );
            let kind = state["connection_kind"].as_str().unwrap_or("").to_owned();
            let mut form = Form::horizontal().label_width(px(140.)).child(
                Field::new()
                    .label("Agent 名称")
                    .child(Input::new(&self.fields[23]).aria_label("Agent 名称")),
            );
            if native {
                let status = agent_status(state, &kind);
                let login_kind = kind.clone();
                form = form
                    .child(Field::new().label("连接状态").child(status))
                    .child(
                        Field::new()
                            .label("登录")
                            .description("用户自行在 Terminal 登录；Pot 不执行登录命令或读取凭据。")
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
                if state["connection_kind"] == "custom" || self.api_advanced {
                    form = form.child(
                        Field::new()
                            .label("服务地址")
                            .description("完整 HTTPS 模型请求地址。")
                            .child(Input::new(&self.fields[14]).aria_label("服务地址")),
                    );
                    let mut formats = div().flex().flex_wrap().gap_2();
                    for (i, (protocol, label)) in [
                        ("openai_chat", "OpenAI 兼容"),
                        ("openai_responses", "Responses"),
                        ("anthropic_messages", "Anthropic"),
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
                    form = form.child(Field::new().label("接口格式").child(formats));
                }
                form = form.child(Field::new().label("API key").description(if state["has_api_key"] == true {"已保存；同一地址留空可保留。"}else{"只需提供服务凭据，Pot 帮你完成配置。"})
                    .child(Input::new(&self.fields[15]).aria_label("API key")))
                    .child(Field::new().label("连接检查").description("只读取模型列表，不发送推理请求；不支持查询时可手动填模型。")
                        .child(Button::new("ai-api-check").label(if busy {"正在处理…"}else{"检查连接并获取模型"}).disabled(busy).on_click({let fields=self.fields.clone();move|_,_,cx|
                            command(json!({"action":"ai-api-check","endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value()}))})))
                    .child(Field::new().label("检查结果").child(div().whitespace_normal().child(state["check_message"].as_str().unwrap_or("").to_owned())));
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
                        "此 CLI 暂不提供已验证的模型列表查询；填写账户支持的模型标识。"
                    } else {
                        "确认查询结果，或填写服务商提供的模型标识。保存不发送模型请求。"
                    })
                    .child(Input::new(&self.fields[16]).aria_label("连接模型")),
            );
            let fields = self.fields.clone();
            body = body.child(card(cx).child(form)
                .child(Button::new("ai-save-connection").primary().label("保存连接").disabled(busy)
                    .on_click(move|_,_,cx|command(json!({"action":"ai-save-connection","name":fields[23].read(cx).value(),"model":fields[16].read(cx).value(),"endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value()})))));
            body = body.child(
                Button::new("ai-advanced")
                    .ghost()
                    .label(if native {
                        "高级调用命令"
                    } else {
                        "高级连接设置"
                    })
                    .on_click(cx.listener(move |this, _, _, cx| {
                        if native {
                            this.settings.commands_open = !this.settings.commands_open;
                        } else {
                            this.settings.api_advanced = !this.settings.api_advanced;
                        }
                        cx.notify();
                    })),
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
                        .child(Input::new(&field).aria_label("聊天模型")),
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
            body=body.child(card(cx).child(title("聊天使用的 Agent"))
                .child(hint(if active["model"].as_str().is_some() {format!("当前生效：{} · {}",state["active_name"].as_str().unwrap_or("Agent"),active["model"].as_str().unwrap_or(""))}else{"尚未配置。添加 Agent 后选择模型，即可开始聊天。".into()},cx))
                .child(form).child(Button::new("ai-save-main").primary().label(if state["return_to_chat"] == true {"保存并开始聊天"}else{"保存更改"})
                    .disabled(busy || profiles.is_empty()).on_click(move|_,_,cx|command(json!({"action":"ai-save-main","model":field.read(cx).value()})))))
                .child(div().flex().justify_between().child(title("已添加的 Agent"))
                    .child(Button::new("ai-add").label("＋ 添加 Agent").disabled(busy).on_click(|_,_,_|command(json!({"action":"ai-screen","screen":"add"})))))
                .child(Button::new("ai-refresh").ghost().label(if state["discovering"] == true {"正在检查…"}else{"刷新连接状态"}).disabled(busy || state["discovering"] == true)
                    .on_click(|_,_,_|command(json!({"action":"agent-refresh"}))));
            if profiles.is_empty() {
                body = body.child(hint(
                    "尚未添加 Agent。可以选择 Codex、Claude 或模型服务。",
                    cx,
                ));
            }
            for (i, p) in profiles.iter().enumerate() {
                let id = p["id"].as_str().unwrap_or("").to_owned();
                body = body.child(
                    card(cx).child(
                        div()
                            .flex()
                            .items_center()
                            .justify_between()
                            .child(
                                div()
                                    .flex()
                                    .flex_col()
                                    .gap_2()
                                    .child(p["name"].as_str().unwrap_or("Agent").to_owned())
                                    .child(hint(
                                        format!(
                                            "{} · {}{}",
                                            agent_status(state, p["engine"].as_str().unwrap_or("")),
                                            p["model"].as_str().unwrap_or(""),
                                            if active["source_profile_id"] == id {
                                                " · 聊天当前使用"
                                            } else {
                                                ""
                                            }
                                        ),
                                        cx,
                                    )),
                            )
                            .child(
                                Button::new(("ai-manage", i))
                                    .label("管理")
                                    .disabled(busy)
                                    .on_click(move |_, _, _| {
                                        command(json!({"action":"ai-manage","id":id}))
                                    }),
                            ),
                    ),
                );
            }
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
