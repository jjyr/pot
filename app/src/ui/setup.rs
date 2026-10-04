use super::*;

impl Settings {
    pub(super) fn render_setup(&self, state: &Value, cx: &mut Context<Shell>) -> Div {
        let stage = state["setup_stage"].as_u64().unwrap_or(0);
        let ready = state["runtime_ready"] == true;
        let selected = state["setup_items"].as_array().cloned().unwrap_or_default();
        let agents = state["agents"].as_array().cloned().unwrap_or_default();
        let prepared = selected.iter().all(|id| {
            id == "api"
                || agents
                    .iter()
                    .any(|a| a["id"] == *id && a["installation"] == "present")
        });
        let usable = selected
            .iter()
            .any(|id| id == "api" || id == "codex" || id == "claude");
        let choice = state["setup_choice"].as_str().unwrap_or("");
        let mut body = div()
            .id("onboarding-scroll")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_3();
        let mut footer = div().flex().items_center().justify_between().gap_2();
        if stage == 0 {
            body = body.child(
                card(cx)
                    .child("Pot 会在独立沙箱中运行 Agent，工作文件会保存在这里。")
                    .child(format!(
                        "环境：{}",
                        if ready {
                            "已就绪"
                        } else {
                            state["environment_phase"].as_str().unwrap_or("等待启动")
                        }
                    ))
                    .child(
                        Button::new("setup-environment")
                            .label(if ready {
                                "刷新工具状态"
                            } else {
                                "准备环境 / 重试"
                            })
                            .on_click(move |_, _, _| {
                                command(json!({"action":if ready {"agent-refresh"} else {"start"}}))
                            }),
                    ),
            );
            body = body.child(
                div()
                    .flex()
                    .gap_2()
                    .child(
                        Button::new("setup-select-all")
                            .ghost()
                            .label("全选")
                            .on_click(|_, _, _| {
                                command(json!({"action":"setup-all","selected":true}))
                            }),
                    )
                    .child(
                        Button::new("setup-select-none")
                            .ghost()
                            .label("取消全选")
                            .on_click(|_, _, _| {
                                command(json!({"action":"setup-all","selected":false}))
                            }),
                    ),
            );
            for (i, (id, name)) in [
                ("codex", "Codex"),
                ("claude", "Claude"),
                ("agy", "AGY（仅 Terminal，尚不能作为主 Agent）"),
                ("api", "OpenAI 兼容 API / Pi"),
            ]
            .into_iter()
            .enumerate()
            {
                let checked = selected.iter().any(|v| v == id);
                let present = id == "api"
                    || agents
                        .iter()
                        .any(|a| a["id"] == id && a["installation"] == "present");
                let status = if id == "api" {
                    "运行环境已随 Pot 提供"
                } else if present {
                    "已安装"
                } else if ready {
                    "未检测到 · 可自行安装后刷新"
                } else {
                    "等待环境检查"
                };
                body = body.child(
                    card(cx)
                        .child(
                            Button::new(("setup-install", i))
                                .ghost()
                                .label(format!("{} {name}", if checked { "☑" } else { "☐" }))
                                .toggled(checked)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"setup-item","id":id}))
                                }),
                        )
                        .child(hint(status, cx)),
                );
            }
            body = body.child(hint(
                "镜像已提供这些运行环境；选择不会触发登录或模型调用。只需登录其中一个即可开始。",
                cx,
            ));
            footer = footer
                .child(
                    Button::new("setup-manual")
                        .ghost()
                        .label("自行安装 / 打开 Terminal")
                        .on_click(|_, _, _| command(json!({"action":"terminal","value":true}))),
                )
                .child(
                    Button::new("setup-next")
                        .primary()
                        .label("登录并开始使用 →")
                        .disabled(!ready || !prepared || !usable || selected.is_empty())
                        .on_click(|_, _, _| command(json!({"action":"setup-stage","value":1}))),
                );
        } else {
            body = body.child(
                div()
                    .flex()
                    .justify_between()
                    .items_center()
                    .child("主 Agent")
                    .child(
                        Button::new("setup-refresh")
                            .ghost()
                            .label("刷新状态")
                            .disabled(state["discovering"] == true)
                            .on_click(|_, _, _| command(json!({"action":"agent-refresh"}))),
                    ),
            );
            for (i, (id, name)) in [("codex", "Codex"), ("claude", "Claude")]
                .into_iter()
                .enumerate()
            {
                let agent = agents.iter().find(|a| a["id"] == id);
                let present = agent.is_some_and(|a| a["installation"] == "present");
                let auth = agent
                    .and_then(|a| a["authentication"].as_str())
                    .unwrap_or("unknown");
                let failed = agent.is_some_and(|a| a["status_check"] == "failed");
                let status = if !present {
                    "未安装"
                } else if state["discovering"] == true {
                    "检测中"
                } else if failed {
                    "检查失败 · 重试"
                } else {
                    match auth {
                        "authenticated" => "已登录",
                        "required" => "未登录 · 在终端登录 ↗",
                        _ => "状态未知 · 在终端登录 ↗",
                    }
                };
                body = body.child(
                    div()
                        .flex()
                        .justify_between()
                        .items_center()
                        .child(
                            Button::new(("setup-main", i))
                                .ghost()
                                .label(format!("{} {name}", if choice == id { "◉" } else { "○" }))
                                .toggled(choice == id)
                                .disabled(!present)
                                .on_click(move |_, _, _| {
                                    command(json!({"action":"setup-choice","id":id}))
                                }),
                        )
                        .child(
                            Button::new(("setup-login", i))
                                .ghost()
                                .label(status)
                                .disabled(!present || auth == "authenticated")
                                .on_click(move |_, _, _| {
                                    command(json!({"action":if failed {"agent-refresh"} else {"agent-login"},"id":id}))
                                }),
                        ),
                );
            }
            body = body.child(
                Button::new("setup-use-api")
                    .ghost()
                    .label(if choice == "api" {
                        "◉ 使用 API（Pi）"
                    } else {
                        "○ 使用 API"
                    })
                    .toggled(choice == "api")
                    .on_click(|_, _, _| command(json!({"action":"setup-choice","id":"api"}))),
            );
            let mut form = Form::horizontal().label_width(px(100.));
            if choice == "api" {
                form = form.child(
                    Field::new().label("格式").child(
                        div().flex().flex_wrap().gap_2().children(
                            [
                                ("openai_chat", "OpenAI 兼容"),
                                ("openai_responses", "Responses"),
                                ("anthropic_messages", "Anthropic"),
                                ("gemini", "Gemini"),
                            ]
                            .into_iter()
                            .enumerate()
                            .map(|(i, (protocol, name))| {
                                Button::new(("setup-api-format", i))
                                    .ghost()
                                    .label(name)
                                    .toggled(state["api_protocol"] == protocol)
                                    .on_click(move |_, _, _| {
                                        command(
                                            json!({"action":"api-protocol","protocol":protocol}),
                                        )
                                    })
                            }),
                        ),
                    ),
                );
                for (i, name) in [(14, "Endpoint"), (15, "API key"), (16, "模型")] {
                    form = form.child(
                        Field::new()
                            .label(name)
                            .child(Input::new(&self.fields[i]).aria_label(name)),
                    );
                }
                body = body.child(form).child(hint(
                    "填写完整 HTTPS 模型请求地址。密钥仅保存在 Sandbox 私有配置；保存不调用模型。",
                    cx,
                ));
            } else if !choice.is_empty() {
                let index = if choice == "claude" { 13 } else { 10 };
                body = body.child(
                    form.child(
                        Field::new()
                            .label("模型")
                            .child(Input::new(&self.fields[index]).aria_label("模型")),
                    ),
                );
                if agents.iter().any(|a| {
                    a["id"] == choice
                        && a["authentication"] == "unknown"
                        && a["status_check"] == "unsupported"
                }) {
                    body = body.child(
                        Button::new("setup-confirm-login")
                            .ghost()
                            .label(if state["setup_unknown_confirmed"] == true {
                                "☑ 我已在终端完成登录（状态未验证）"
                            } else {
                                "☐ 我已在终端完成登录（状态未验证）"
                            })
                            .on_click(|_, _, _| command(json!({"action":"setup-confirm"}))),
                    );
                }
            }
            let allowed = ready
                && (choice == "api"
                    || agents.iter().any(|a| {
                        a["id"] == choice
                            && (a["authentication"] == "authenticated"
                                || (a["authentication"] == "unknown"
                                    && a["status_check"] == "unsupported"
                                    && state["setup_unknown_confirmed"] == true))
                    }));
            let fields = self.fields.clone();
            let engine = choice.to_owned();
            footer=footer.child(Button::new("setup-back").ghost().label("← 返回准备工具").on_click(|_,_,_|command(json!({"action":"setup-stage","value":0}))))
                .child(Button::new("setup-start").primary().label(format!("使用 {} 开始",match choice {"codex"=>"Codex","claude"=>"Claude","api"=>"API",_=>"Agent"})).disabled(!allowed)
                    .on_click(move|_,_,cx|{
                        if engine=="api" {command(json!({"action":"save-api","endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value(),"model":fields[16].read(cx).value()}));}
                        else {let index=if engine=="claude"{13}else{10};command(json!({"action":format!("select-{engine}"),"model":fields[index].read(cx).value()}));}
                    }));
        }
        let mut column = content()
            .child(
                title(if stage == 0 {
                    "准备工具"
                } else {
                    "登录并开始使用"
                })
                .h(px(32.)),
            )
            .child(div().h(px(20.)).child(if stage == 0 {
                "选择要准备的工具，安装与登录互不影响。"
            } else if choice == "api" {
                "填写 API 连接后即可开始；保存不会发送测试消息。"
            } else {
                "在终端中登录至少一个 Agent，然后选择它开始使用。"
            }));
        if stage == 1 && choice != "api" {
            column = column.child(div().h(px(220.)).flex_shrink_0());
        }
        page(cx).child(
            column
                .child(body)
                .child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx))
                .child(footer),
        )
    }
}
