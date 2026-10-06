use super::*;

impl Settings {
    pub(super) fn render_setup(&self, state: &Value, cx: &mut Context<Shell>) -> Div {
        let ready = state["runtime_ready"] == true;
        let selected = state["setup_items"].as_array().cloned().unwrap_or_default();
        let agents = state["agents"].as_array().cloned().unwrap_or_default();
        let mut body = div()
            .id("onboarding-scroll")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_3();
        let mut footer = div().flex().items_center().justify_between().gap_2();
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
                        .on_click(|_, _, _| command(json!({"action":"setup-all","selected":true}))),
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
            ("grok", "Grok"),
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
            "镜像已提供这些运行环境；选择不会触发登录或模型调用。缺失工具请更新或重新准备环境。",
            cx,
        ));
        footer = footer
            .child(
                Button::new("setup-manual")
                    .ghost()
                    .label("稍后设置")
                    .on_click(|_, _, _| command(json!({"action":"setup-later"}))),
            )
            .child(
                Button::new("setup-next")
                    .primary()
                    .label("选择主 Agent →")
                    .disabled(!ready)
                    .on_click(|_, _, _| command(json!({"action":"setup-stage","value":1}))),
            );
        footer = footer.child(
            Button::new("setup-later")
                .ghost()
                .label("稍后设置")
                .on_click(|_, _, _| command(json!({"action":"setup-later"}))),
        );
        page(cx).child(
            content()
                .child(title("准备环境与工具"))
                .child(body)
                .child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx))
                .child(footer),
        )
    }
}
