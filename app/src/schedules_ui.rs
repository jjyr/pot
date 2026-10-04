use crate::{Shell, client, gpui, ui};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable,
    button::{Button, ButtonVariants},
    form::{Field, Form},
    input::{Input, InputState},
};
use pot_protocol::ScheduleInfo;
use serde_json::json;

pub fn navigation(scheduled: bool, cx: &mut Context<Shell>) -> Div {
    div()
        .flex()
        .gap_2()
        .children([(false, "任务"), (true, "定时计划")].map(|(value, label)| {
            Button::new(if value { "nav-schedules" } else { "nav-tasks" })
                .ghost()
                .label(label)
                .when(value == scheduled, |b| b.bg(cx.theme().secondary))
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.schedule_tab = value;
                    cx.notify();
                }))
        }))
}
pub struct SchedulesUI {
    fields: Vec<Entity<InputState>>,
    pub open: bool,
    editing: Option<String>,
    kind: String,
    notify: String,
    misfire: String,
    enabled: bool,
    pub success: u64,
    error: String,
    delete_confirm: bool,
}
impl SchedulesUI {
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        Self {
            fields: (0..7)
                .map(|_| cx.new(|cx| InputState::new(window, cx)))
                .collect(),
            open: false,
            editing: None,
            kind: "daily".into(),
            notify: "changes".into(),
            misfire: "coalesce".into(),
            enabled: true,
            success: 0,
            error: String::new(),
            delete_confirm: false,
        }
    }
    fn edit(&mut self, plan: Option<&ScheduleInfo>, window: &mut Window, cx: &mut Context<Shell>) {
        self.open = true;
        self.error.clear();
        self.delete_confirm = false;
        self.editing = plan.map(|p| p.id.clone());
        let defaults = vec![
            "".into(),
            "".into(),
            "Asia/Shanghai".into(),
            "".into(),
            "60".into(),
            "09:00".into(),
            "1,2,3,4,5".into(),
        ];
        let values = plan
            .map(|p| {
                vec![
                    p.spec.title.clone(),
                    p.spec.goal.clone(),
                    p.spec.timezone.clone(),
                    p.spec.at.clone(),
                    p.spec.every_minutes.to_string(),
                    p.spec.time.clone(),
                    p.spec
                        .weekdays
                        .iter()
                        .map(|d| d.to_string())
                        .collect::<Vec<_>>()
                        .join(","),
                ]
            })
            .unwrap_or(defaults);
        for (f, value) in self.fields.iter().zip(values) {
            f.update(cx, |f, cx| f.set_value(value, window, cx));
        }
        self.kind = plan.map(|p| p.spec.kind.clone()).unwrap_or("daily".into());
        self.notify = plan
            .map(|p| p.spec.notify.clone())
            .unwrap_or("changes".into());
        self.misfire = plan
            .map(|p| p.spec.misfire.clone())
            .unwrap_or("coalesce".into());
        self.enabled = plan.map(|p| p.spec.enabled).unwrap_or(true);
    }
    pub fn render(&self, v: &client::View, cx: &mut Context<Shell>) -> Div {
        let mut body = ui::content()
            .child(navigation(true, cx))
            .child(
                div()
                    .flex()
                    .justify_between()
                    .items_center()
                    .child(ui::title(if self.open {
                        "编辑定时计划"
                    } else {
                        "定时计划"
                    }))
                    .when(!self.open, |el| {
                        el.child(
                            Button::new("new-schedule")
                                .primary()
                                .label("新建计划")
                                .on_click(cx.listener(|this, _, window, cx| {
                                    this.schedules_ui.edit(None, window, cx);
                                    cx.notify();
                                })),
                        )
                    }),
            )
            .child(ui::hint(
                "隐藏窗口后仍可执行；Quit、睡眠或关机期间不执行，恢复后按计划补跑或跳过。",
                cx,
            ));
        if !v.schedule_error.is_empty() || !self.error.is_empty() {
            body = body.child(ui::hint(format!("{} {}", self.error, v.schedule_error), cx));
        }
        if self.open {
            let input = |i, name, description| {
                Field::new()
                    .label(name)
                    .description(description)
                    .child(Input::new(&self.fields[i]).aria_label(name))
            };
            let mut form = Form::horizontal()
                .label_width(px(140.))
                .child(input(0, "名称", "简短说明这个计划"))
                .child(input(1, "目标", "每次执行的目标、数据来源和通知条件"))
                .child(input(
                    2,
                    "时区",
                    "IANA 名称，如 Asia/Shanghai 或 America/New_York",
                ))
                .child(
                    Field::new().label("频率").child(
                        div().flex().gap_1().children(
                            [
                                ("once", "一次"),
                                ("interval", "间隔"),
                                ("daily", "每天"),
                                ("weekly", "每周"),
                            ]
                            .map(|(value, label)| {
                                Button::new(SharedString::from(format!("kind-{value}")))
                                    .ghost()
                                    .label(label)
                                    .when(self.kind == value, |b| b.bg(cx.theme().secondary))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.schedules_ui.kind = value.into();
                                        cx.notify();
                                    }))
                            }),
                        ),
                    ),
                );
            match self.kind.as_str() {
                "once" => {
                    form = form.child(input(3, "运行时间", "所选时区本地时间：YYYY-MM-DD HH:MM"))
                }
                "interval" => {
                    form = form.child(input(4, "间隔分钟", "1–525600；按实际经过的时间计算"))
                }
                _ => {
                    form = form.child(input(5, "每天时间", "HH:MM，按所选时区的日历时间"));
                    if self.kind == "weekly" {
                        form = form.child(input(
                            6,
                            "星期",
                            "1=周一，7=周日；英文逗号分隔，如 1,2,3,4,5",
                        ));
                    }
                }
            }
            form = form
                .child(
                    Field::new()
                        .label("错过运行")
                        .child(div().flex().gap_2().children(
                            [("coalesce", "最多补跑一次"), ("skip", "跳过")].map(
                                |(value, label)| {
                                    Button::new(SharedString::from(format!("miss-{value}")))
                                        .ghost()
                                        .label(label)
                                        .when(self.misfire == value, |b| b.bg(cx.theme().secondary))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.schedules_ui.misfire = value.into();
                                            cx.notify();
                                        }))
                                },
                            ),
                        )),
                )
                .child(
                    Field::new().label("通知").child(
                        div().flex().gap_1().children(
                            [
                                ("changes", "变化或失败"),
                                ("all", "每次"),
                                ("failures", "仅失败"),
                            ]
                            .map(|(value, label)| {
                                Button::new(SharedString::from(format!("notify-{value}")))
                                    .ghost()
                                    .label(label)
                                    .when(self.notify == value, |b| b.bg(cx.theme().secondary))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.schedules_ui.notify = value.into();
                                        cx.notify();
                                    }))
                            }),
                        ),
                    ),
                )
                .child(
                    Field::new().label("状态").child(
                        Button::new("schedule-enabled")
                            .ghost()
                            .label(if self.enabled { "启用" } else { "暂停" })
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.schedules_ui.enabled = !this.schedules_ui.enabled;
                                cx.notify();
                            })),
                    ),
                );
            body=body.child(div().id("schedule-editor-scroll").flex_1().min_h_0().overflow_y_scroll().child(ui::card(cx).child(form)))
                .child(div().flex().gap_2()
                    .child(Button::new("save-schedule").primary().label("保存计划").disabled(v.schedule_pending).on_click(cx.listener(|this,_,_,cx| {
                        let e=&mut this.schedules_ui;let values:Vec<String>=e.fields.iter().map(|f|f.read(cx).value().to_string()).collect();
                        let minutes=if e.kind=="interval" {match values[4].parse::<u64>() {Ok(n)=>n,Err(_)=>{e.error="间隔必须是整数分钟".into();cx.notify();return;}}}else{60};
                        let days:Vec<u32>=if e.kind=="weekly" {match values[6].split(',').map(|s|s.trim().parse()).collect::<Result<Vec<_>,_>>() {Ok(v)=>v,Err(_)=>{e.error="星期格式应为 1,2,3,4,5".into();cx.notify();return;}}}else{vec![]};
                        this.client.schedule_action(json!({"action":if e.editing.is_some(){"update"}else{"create"},"schedule_id":e.editing,"spec":{"title":values[0],"goal":values[1],"timezone":values[2],"kind":e.kind,"at":values[3],"every_minutes":minutes,"time":values[5],"weekdays":days,"misfire":e.misfire,"notify":e.notify,"enabled":e.enabled}}));cx.notify();
                    })))
                    .child(Button::new("cancel-schedule-edit").ghost().label("取消").disabled(v.schedule_pending).on_click(cx.listener(|this,_,_,cx|{this.schedules_ui.open=false;cx.notify();})))
                );
            return ui::page(cx).child(body);
        }
        if let Some(plan) = &v.schedules.selected {
            let id = plan.id.clone();
            let run_id = id.clone();
            let toggle_id = id.clone();
            let delete_id = id.clone();
            let action = if plan.spec.enabled { "pause" } else { "resume" };
            body=body.child(Button::new("back-schedules").ghost().label("‹ 所有计划").on_click(cx.listener(|this,_,_,cx|{this.client.schedule_select(String::new());this.schedules_ui.delete_confirm=false;cx.notify();})))
                .child(ui::card(cx).child(ui::title(plan.spec.title.clone()))
                    .child(ui::hint(rule(plan),cx)).child(ui::hint(plan.spec.goal.clone(),cx))
                    .child(ui::hint(format!("下次：{}",if plan.next_label.is_empty(){"未安排"}else{&plan.next_label}),cx))
                    .when(!plan.last_error.is_empty(),|el|el.child(ui::hint(format!("调度错误：{}",plan.last_error),cx)))
                    .child(div().flex().gap_2()
                        .child(Button::new("schedule-run").primary().label("立即运行").disabled(v.schedule_pending).on_click(cx.listener(move|this,_,_,cx|{this.client.schedule_action(json!({"action":"run","schedule_id":run_id}));cx.notify();})))
                        .child(Button::new("schedule-pause").ghost().label(if plan.spec.enabled {"暂停"}else{"恢复"}).disabled(v.schedule_pending).on_click(cx.listener(move|this,_,_,cx|{this.client.schedule_action(json!({"action":action,"schedule_id":toggle_id}));cx.notify();})))
                        .child(Button::new("schedule-edit").ghost().label("编辑").disabled(v.schedule_pending).on_click(cx.listener(|this,_,window,cx|{let plan=this.client.view.lock().unwrap().schedules.selected.clone();this.schedules_ui.edit(plan.as_ref(),window,cx);cx.notify();})))
                        .child(Button::new("schedule-delete").ghost().label(if self.delete_confirm {"确认移除计划"}else{"移除"}).disabled(v.schedule_pending).on_click(cx.listener(move|this,_,_,cx|{if this.schedules_ui.delete_confirm {this.client.schedule_action(json!({"action":"delete","schedule_id":delete_id}));this.schedules_ui.delete_confirm=false;}else{this.schedules_ui.delete_confirm=true;}cx.notify();})))
                    ).child(ui::hint("暂停或移除只影响未来触发，不会停止当前执行。首次成功观测视为变化。",cx))
                )
                .child(ui::hint("执行历史 · 最近 100 次 · 点击有任务的记录查看详情",cx))
                .child(div().id("schedule-runs").flex_1().min_h_0().overflow_y_scroll().children(v.schedules.runs.iter().map(|run| {
                    let task=run.task_id.clone();
                    ui::card(cx).mb_3().child(ui::hint(format!("{} · {}",run.scheduled_label,state(&run.state)),cx))
                        .child(ui::hint(run.summary.clone(),cx))
                        .when(task.is_some(),|el|el.child(Button::new(SharedString::from(format!("run-{}",run.id))).ghost().label("查看任务 →").on_click(cx.listener(move|this,_,_,cx|{this.show_tasks=false;this.client.select(task.clone().unwrap());this.first=true;cx.notify();}))))
                })))
                .child(div().flex().gap_2()
                    .child(Button::new("runs-newer").ghost().label("较新记录").disabled(v.runs_offset==0).on_click(cx.listener(|this,_,_,cx|{let mut v=this.client.view.lock().unwrap();v.runs_offset=v.runs_offset.saturating_sub(20);cx.notify();})))
                    .child(Button::new("runs-older").ghost().label("较早记录").disabled(v.schedules.runs_next_offset.is_none()).on_click(cx.listener(|this,_,_,cx|{let mut v=this.client.view.lock().unwrap();if let Some(offset)=v.schedules.runs_next_offset{v.runs_offset=offset;}cx.notify();})))
                );
        } else {
            body = body
                .child(
                    div()
                        .id("schedule-list")
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .when(v.schedules.items.is_empty(), |el| {
                            el.child(ui::hint(
                                "暂无计划。可以在这里新建，也可以直接告诉 Pot 何时做什么。",
                                cx,
                            ))
                        })
                        .children(v.schedules.items.iter().map(|plan| {
                            let id = plan.id.clone();
                            ui::card(cx)
                                .mb_3()
                                .child(
                                    Button::new(SharedString::from(format!(
                                        "schedule-{}",
                                        plan.id
                                    )))
                                    .ghost()
                                    .label(plan.spec.title.clone())
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.client.schedule_select(id.clone());
                                        this.schedules_ui.delete_confirm = false;
                                        cx.notify();
                                    })),
                                )
                                .child(ui::hint(rule(plan), cx))
                                .child(ui::hint(
                                    format!(
                                        "下次：{}",
                                        if plan.next_label.is_empty() {
                                            "未安排"
                                        } else {
                                            &plan.next_label
                                        }
                                    ),
                                    cx,
                                ))
                        })),
                )
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(ui::hint(format!("共 {} 个计划", v.schedules.total), cx))
                        .child(
                            Button::new("schedule-prev")
                                .ghost()
                                .label("上一页")
                                .disabled(v.schedule_offset == 0)
                                .on_click(cx.listener(|this, _, _, cx| {
                                    let mut v = this.client.view.lock().unwrap();
                                    v.schedule_offset = v.schedule_offset.saturating_sub(20);
                                    cx.notify();
                                })),
                        )
                        .child(
                            Button::new("schedule-next")
                                .ghost()
                                .label("下一页")
                                .disabled(v.schedules.next_offset.is_none())
                                .on_click(cx.listener(|this, _, _, cx| {
                                    let mut v = this.client.view.lock().unwrap();
                                    if let Some(offset) = v.schedules.next_offset {
                                        v.schedule_offset = offset;
                                    }
                                    cx.notify();
                                })),
                        ),
                );
        }
        ui::page(cx).child(body)
    }
}
fn rule(p: &ScheduleInfo) -> String {
    let frequency = match p.spec.kind.as_str() {
        "once" => format!("一次 · {}", p.spec.at),
        "interval" => format!("每 {} 分钟", p.spec.every_minutes),
        "weekly" => format!("每周 {:?} · {}", p.spec.weekdays, p.spec.time),
        _ => format!("每天 {}", p.spec.time),
    };
    format!(
        "{} · {} · {}",
        if p.spec.enabled {
            "已启用"
        } else if p.spec.kind == "once" && p.next_at_ms.is_none() {
            "未启用 / 已结束"
        } else {
            "已暂停"
        },
        frequency,
        p.spec.timezone
    )
}
fn state(s: &str) -> &str {
    match s {
        "queued" => "排队中",
        "running" => "执行中",
        "complete" => "已完成",
        "failed" => "失败",
        "interrupted" => "已中断",
        "cancelled" => "已取消",
        "skipped_overlap" => "跳过：上次尚未结束",
        "skipped_missed" => "跳过：错过运行",
        _ => s,
    }
}
