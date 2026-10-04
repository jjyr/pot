use super::*;

impl Settings {
    pub(super) fn routing_form(&self, state: &Value, _cx: &mut Context<Shell>) -> Form {
        let busy = state["routing_busy"] == true;
        let selected = state["routing_profile"].as_str().unwrap_or("");
        let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
        let mut form = Form::horizontal().label_width(px(160.)).child(
            Field::new().label("已保存的配置").child(
                Button::new("routing-refresh")
                    .ghost()
                    .label("刷新 / 放弃修改")
                    .disabled(busy)
                    .on_click(|_, _, _| command(json!({"action":"routing-refresh"}))),
            ),
        );
        for (i, profile) in profiles.iter().enumerate() {
            let id = profile["id"].as_str().unwrap_or("").to_owned();
            let worker_id = id.clone();
            let hint_id = id.clone();
            let allowed = state["routing"]["allowed_tool_profiles"]
                .as_array()
                .is_some_and(|v| v.iter().any(|p| p == &id));
            form = form.child(Field::new().label(profile["name"].as_str().unwrap_or("").to_owned())
                .description(format!("{} · {}", profile["engine"].as_str().unwrap_or(""),profile["model"].as_str().unwrap_or("")))
                .child(div().flex().flex_wrap().gap_2()
                    .child(Button::new(("profile-edit",i)).ghost().label("编辑").toggled(selected == id).disabled(busy)
                        .on_click(move|_,_,_|command(json!({"action":"routing-edit","id":id}))))
                    .child(Button::new(("profile-worker",i)).ghost().label("后台默认").toggled(state["routing"]["worker_profile"] == worker_id).disabled(busy)
                        .on_click(move|_,_,_|command(json!({"action":"routing-choice","kind":"worker","id":worker_id}))))
                    .child(Button::new(("profile-hint",i)).ghost().label(if allowed {"☑ 允许 Agent 指定"}else{"☐ 允许 Agent 指定"}).disabled(busy)
                        .on_click(move|_,_,_|command(json!({"action":"routing-choice","kind":"hint","id":hint_id}))))));
        }
        if profiles.is_empty() {
            return form.child(
                Field::new()
                    .label("尚无配置")
                    .child("先在 Agent 设置中保存一个后端。"),
            );
        }
        form = form.child(
            Field::new()
                .label("后台默认")
                .description("未明确指定 profile 的新任务使用此设置；计划自己的选择优先。")
                .child(
                    Button::new("worker-follow")
                        .ghost()
                        .label("跟随最近的 Agent 设置")
                        .toggled(state["routing"]["worker_profile"].is_null())
                        .disabled(busy)
                        .on_click(|_, _, _| {
                            command(json!({"action":"routing-choice","kind":"worker","id":""}))
                        }),
                ),
        );
        for (index, label, description) in [
            (
                17,
                "配置名称",
                "保存当前配置，或复制为独立配置；已有会话保持固定。",
            ),
            (18, "模型", "使用该后端支持的模型标识；保存不调用模型。"),
            (19, "此配置并发上限", "1–1000，留空表示仅受全局上限约束。"),
            (
                20,
                "此服务商并发上限",
                "同一服务商的配置共享上限；1–1000，留空取消此限制。",
            ),
        ] {
            form = form.child(
                Field::new()
                    .label(label)
                    .description(description)
                    .child(Input::new(&self.fields[index]).aria_label(label)),
            );
        }
        form = form.child(Field::new().label("无人值守权限").child("Agent 可在 Sandbox 内执行文件和命令操作；主 Agent 可创建任务，worker 不可委派，汇总只读。额外权限请求会被拒绝并显示在会话中。"));
        form = form.child(
            Field::new().label("迁移对象").child(
                state["routing_owner_name"]
                    .as_str()
                    .unwrap_or("主会话")
                    .to_owned(),
            ),
        );
        let quota = &state["provider_allowance"];
        if quota["enforcement"] == "pre_request" {
            let mut periods = div().flex().gap_2();
            for (i, (period, name)) in [("none", "不限"), ("day", "每天"), ("week", "每周")]
                .into_iter()
                .enumerate()
            {
                periods = periods.child(
                    Button::new(("provider-period", i))
                        .ghost()
                        .label(name)
                        .toggled(state["provider_period"] == period)
                        .disabled(busy)
                        .on_click(move |_, _, _| {
                            command(json!({"action":"provider-period","period":period}))
                        }),
                );
            }
            form = form
                .child(Field::new().label("服务商 Token 周期").child(periods))
                .child(
                    Field::new()
                        .label("服务商 Token 额度")
                        .description(format!(
                            "与全局额度同时生效；时区 {}。当前使用 {}，预留 {}。",
                            quota["policy"]["timezone"]
                                .as_str()
                                .unwrap_or("Asia/Shanghai"),
                            quota["used"].as_u64().unwrap_or(0),
                            quota["reserved"].as_u64().unwrap_or(0)
                        ))
                        .child(Input::new(&self.fields[21]).aria_label("服务商 Token 额度")),
                )
                .child(
                    Field::new()
                        .label("额度恢复")
                        .description("自动继续还要求引擎支持安全检查点；服务商自身的限流独立处理。")
                        .child(
                            Button::new("provider-auto")
                                .ghost()
                                .label(if state["provider_auto_resume"] == true {
                                    "☑ 自动继续"
                                } else {
                                    "☐ 自动继续"
                                })
                                .disabled(busy)
                                .on_click(|_, _, _| command(json!({"action":"provider-auto"}))),
                        ),
                );
            let fields = self.fields.clone();
            form = form.child(Field::new().label("服务商额度").child(Button::new("provider-save").label("保存服务商额度").disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"provider-save","tokens":fields[21].read(cx).value()})))));
        } else {
            form = form.child(
                Field::new()
                    .label("服务商 Token 额度")
                    .child("CLI 登录路线仅统计原生报告的用量；无法在每次模型请求前执行硬额度。"),
            );
        }
        let mut actions = div().flex().flex_wrap().gap_2();
        for (i, copy) in [false, true].into_iter().enumerate() {
            let fields = self.fields.clone();
            actions = actions.child(Button::new(("save-profile",i)).label(if copy {"复制为新配置"}else{"保存配置"}).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"profile-save","copy":copy,"name":fields[17].read(cx).value(),"model":fields[18].read(cx).value()}))));
        }
        actions = actions.child(
            Button::new("profile-main")
                .label(if state["routing_owner"] == "main" {
                    "将已保存配置用于主会话"
                } else {
                    "将已保存配置用于此任务"
                })
                .disabled(busy)
                .on_click(|_, _, _| command(json!({"action":"profile-main"}))),
        );
        let fields = self.fields.clone();
        form.child(Field::new().label("配置操作").child(actions))
            .child(Field::new().label("任务路由").child(Button::new("save-routing").primary().label("保存默认、白名单和并发限制").disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"routing-save","profile_limit":fields[19].read(cx).value(),"provider_limit":fields[20].read(cx).value()})))))
    }
}
