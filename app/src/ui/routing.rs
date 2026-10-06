use super::*;
use gpui_kit::component::menu::{DropdownMenu, PopupMenuItem};

impl Settings {
    pub(super) fn routing_form(&self, state: &Value, _cx: &mut Context<Shell>) -> Form {
        let busy = state["routing_busy"] == true;
        let selected = state["routing_profile"].as_str().unwrap_or("");
        let profiles = state["profiles"].as_array().cloned().unwrap_or_default();
        let mut form = form(_cx).child(
            field("已保存的配置").child(
                Button::new("routing-refresh")
                    .ghost()
                    .small()
                    .small()
                    .label("刷新 / 放弃修改")
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
            .unwrap_or_else(|| "选择独立 Agent".into());
        let details_label = profiles
            .iter()
            .find(|p| p["id"] == selected)
            .and_then(|p| p["name"].as_str())
            .unwrap_or("选择要查看的 Agent")
            .to_owned();
        form = form.child(
            field("后台任务 Agent")
                .description("默认沿用聊天配置；独立选择只影响新任务。已有任务保持固定。")
                .child(
                    Button::new("worker-follow-chat")
                        .small()
                        .label(if independent {
                            "改为沿用聊天配置"
                        } else {
                            "沿用聊天配置"
                        })
                        .toggled(!independent)
                        .disabled(busy)
                        .on_click(|_, _, _| {
                            command(json!({"action":"routing-choice","kind":"worker","id":""}))
                        }),
                ),
        );
        if state["routing_owner"] == "main" {
            form = form.child(field("独立任务配置").child(Button::new("worker-pick").small().label(worker_label).dropdown_caret(true).disabled(busy)
                .dropdown_menu(move|menu,_,_| worker_choices.iter().fold(menu,|menu,p| {let id=p["id"].as_str().unwrap_or("").to_owned();
                    menu.item(PopupMenuItem::new(p["name"].as_str().unwrap_or("Agent").to_owned()).on_click(move|_,_,_|command(json!({"action":"routing-choice","kind":"worker","id":id}))))}))));
        }
        form = form.child(
            field("配置详情").child(
                Button::new("routing-details")
                    .small()
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
            return form.child(field("尚无配置").child("先在 AI 配置中添加一个 Agent。"));
        }
        if state["routing_owner"] != "main" {
            let id = selected.to_owned();
            form = form.child(
                field("连接与登录").child(
                    Button::new("task-ai-manage")
                        .small()
                        .label("管理所选 Agent 的连接")
                        .on_click(move |_, _, _| command(json!({"action":"ai-manage","id":id}))),
                ),
            );
        }
        form = form.child(
            field("高级路由").child(
                Button::new("routing-advanced")
                    .ghost()
                    .small()
                    .label(if self.advanced {
                        "收起高级路由"
                    } else {
                        "展开高级路由、权限与额度"
                    })
                    .on_click(_cx.listener(|this, _, _, cx| {
                        this.settings.advanced = !this.settings.advanced;
                        cx.notify();
                    })),
            ),
        );
        if !self.advanced && state["routing_owner"] == "main" {
            let fields = self.fields.clone();
            return form.child(field("任务配置").child(Button::new("routing-save-simple").primary().small().label("保存任务配置").disabled(busy)
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
                            "☑ 允许 Agent 指定"
                        } else {
                            "☐ 允许 Agent 指定"
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
                field(label)
                    .description(description)
                    .child(Input::new(&self.fields[index]).aria_label(label)),
            );
        }
        form = form.child(field("无人值守权限").child(div().w_full().min_w_0().whitespace_normal().child("Agent 可在 Sandbox 内执行文件和命令操作；主 Agent 可创建任务，worker 不可委派，汇总只读。额外权限请求会被拒绝并显示在会话中。")));
        form = form.child(
            field("迁移对象").child(
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
                .child(field("服务商 Token 周期").child(periods))
                .child(
                    field("服务商 Token 额度")
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
                    field("额度恢复")
                        .description("自动继续还要求引擎支持安全检查点；服务商自身的限流独立处理。")
                        .child(
                            Button::new("provider-auto")
                                .ghost()
                                .small()
                                .small()
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
            form = form.child(field("服务商额度").child(Button::new("provider-save").small().label("保存服务商额度").disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"provider-save","tokens":fields[21].read(cx).value()})))));
        } else {
            form =
                form.child(field("服务商 Token 额度").child(
                    div().w_full().min_w_0().whitespace_normal().child(
                        "CLI 登录路线仅统计原生报告的用量；无法在每次模型请求前执行硬额度。",
                    ),
                ));
        }
        let mut actions = div().flex().flex_wrap().gap_2();
        for (i, copy) in [false, true].into_iter().enumerate() {
            let fields = self.fields.clone();
            actions = actions.child(Button::new(("save-profile",i)).small().label(if copy {"复制为新配置"}else{"保存配置"}).disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"profile-save","copy":copy,"name":fields[17].read(cx).value(),"model":fields[18].read(cx).value()}))));
        }
        if state["routing_owner"] != "main" {
            actions = actions.child(
                Button::new("profile-main")
                    .small()
                    .label("保存并切换此任务的 Agent")
                    .disabled(busy)
                    .on_click(|_, _, _| command(json!({"action":"profile-main"}))),
            );
        }
        let fields = self.fields.clone();
        form.child(field("配置操作").child(actions))
            .child(field("任务路由").child(Button::new("save-routing").primary().small().small().label("保存默认、白名单和并发限制").disabled(busy)
                .on_click(move|_,_,cx|command(json!({"action":"routing-save","profile_limit":fields[19].read(cx).value(),"provider_limit":fields[20].read(cx).value()})))))
    }
}
