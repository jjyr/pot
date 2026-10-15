use crate::application::{Shell, client, gpui, ui};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, Sizable,
    button::{Button, ButtonVariants},
    input::{Input, InputState},
};
use pot_protocol::ScheduleInfo;
use serde_json::json;

pub fn navigation(scheduled: bool, cx: &mut Context<Shell>) -> Div {
    ui::segments(cx).children(
        [
            (false, crate::application::i18n::text("navigation.tasks")),
            (true, crate::application::i18n::text("navigation.schedules")),
        ]
        .map(|(value, label)| {
            Button::new(if value { "nav-schedules" } else { "nav-tasks" })
                .ghost()
                .small()
                .label(label)
                .toggled(value == scheduled)
                .when(value == scheduled, |b| {
                    b.bg(pot_ui::design::surface(cx)).shadow_sm()
                })
                .on_click(cx.listener(move |this, _, _, cx| {
                    this.schedule_tab = value;
                    ui::command(json!({"action":"page","value":3,"scheduled":value}));
                    cx.notify();
                }))
        }),
    )
}
pub struct SchedulesUI {
    pub scroll: ScrollHandle,
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
            scroll: ScrollHandle::new(),
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
        let mut body = ui::content(cx)
            .child(navigation(true, cx))
            .when(self.open, |el| {
                el.child(ui::section(crate::application::i18n::text(
                    "schedules-ui.edit-schedule",
                )))
            })
            .child(ui::hint(
                crate::application::i18n::text("schedules-ui.describe-a-goal-and-time-in-the"),
                cx,
            ));
        if !v.schedule_error.is_empty() || !self.error.is_empty() {
            body = body.child(ui::notice(
                format!(
                    "{} {}",
                    crate::application::i18n::message(&self.error),
                    crate::application::i18n::message(&v.schedule_error)
                ),
                true,
                cx,
            ));
        }
        if self.open {
            let input = |i, name, description| {
                ui::field(name)
                    .description(description)
                    .child(Input::new(&self.fields[i]).aria_label(name))
            };
            let mut form = ui::form(cx)
                .child(input(
                    0,
                    crate::application::i18n::text("schedules-ui.name"),
                    crate::application::i18n::text(
                        "schedules-ui.a-short-description-of-this-schedule",
                    ),
                ))
                .child(input(
                    1,
                    crate::application::i18n::text("schedules-ui.goal"),
                    crate::application::i18n::text(
                        "schedules-ui.goal-data-sources-and-notification-conditions-for",
                    ),
                ))
                .child(input(
                    2,
                    crate::application::i18n::text("schedules-ui.time-zone"),
                    crate::application::i18n::text(
                        "schedules-ui.iana-name-such-as-asia-shanghai-or",
                    ),
                ))
                .child(
                    ui::field(crate::application::i18n::text("schedules-ui.frequency")).child(
                        ui::segments(cx).children(
                            [
                                ("once", crate::application::i18n::text("schedules-ui.once")),
                                (
                                    "interval",
                                    crate::application::i18n::text("schedules-ui.interval"),
                                ),
                                (
                                    "daily",
                                    crate::application::i18n::text("schedules-ui.daily"),
                                ),
                                (
                                    "weekly",
                                    crate::application::i18n::text("schedules-ui.weekly"),
                                ),
                            ]
                            .map(|(value, label)| {
                                Button::new(SharedString::from(format!("kind-{value}")))
                                    .ghost()
                                    .small()
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
                    form = form.child(input(
                        3,
                        crate::application::i18n::text("schedules-ui.run-at"),
                        crate::application::i18n::text(
                            "schedules-ui.local-time-in-the-selected-zone-yyyy",
                        ),
                    ))
                }
                "interval" => {
                    form = form.child(input(
                        4,
                        crate::application::i18n::text("schedules-ui.interval-in-minutes"),
                        crate::application::i18n::text(
                            "schedules-ui.1-525600-measured-by-elapsed-time",
                        ),
                    ))
                }
                _ => {
                    form = form.child(input(
                        5,
                        crate::application::i18n::text("schedules-ui.time-of-day"),
                        crate::application::i18n::text(
                            "schedules-ui.hh-mm-using-calendar-time-in-the",
                        ),
                    ));
                    if self.kind == "weekly" {
                        form = form.child(input(
                            6,
                            crate::application::i18n::text("schedules-ui.days-of-week"),
                            crate::application::i18n::text(
                                "schedules-ui.1-monday-7-sunday-comma-separated-such",
                            ),
                        ));
                    }
                }
            }
            form = form
                .child(
                    ui::field(crate::application::i18n::text("schedules-ui.missed-runs")).child(
                        ui::segments(cx).children(
                            [
                                (
                                    "coalesce",
                                    crate::application::i18n::text(
                                        "schedules-ui.run-once-to-catch-up",
                                    ),
                                ),
                                ("skip", crate::application::i18n::text("schedules-ui.skip")),
                            ]
                            .map(|(value, label)| {
                                Button::new(SharedString::from(format!("miss-{value}")))
                                    .ghost()
                                    .small()
                                    .label(label)
                                    .when(self.misfire == value, |b| b.bg(cx.theme().secondary))
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.schedules_ui.misfire = value.into();
                                        cx.notify();
                                    }))
                            }),
                        ),
                    ),
                )
                .child(
                    ui::field(crate::application::i18n::text("schedules-ui.notifications")).child(
                        ui::segments(cx).children(
                            [
                                (
                                    "changes",
                                    crate::application::i18n::text(
                                        "schedules-ui.changes-or-failures",
                                    ),
                                ),
                                (
                                    "all",
                                    crate::application::i18n::text("schedules-ui.every-run"),
                                ),
                                (
                                    "failures",
                                    crate::application::i18n::text("schedules-ui.failures-only"),
                                ),
                            ]
                            .map(|(value, label)| {
                                Button::new(SharedString::from(format!("notify-{value}")))
                                    .ghost()
                                    .small()
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
                    ui::field(crate::application::i18n::text("schedules-ui.status")).child(
                        Button::new("schedule-enabled")
                            .ghost()
                            .small()
                            .label(if self.enabled {
                                crate::application::i18n::text("schedules-ui.enabled")
                            } else {
                                crate::application::i18n::text("schedules-ui.paused")
                            })
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.schedules_ui.enabled = !this.schedules_ui.enabled;
                                cx.notify();
                            })),
                    ),
                );
            body=body.child(div().id("schedule-editor-scroll").track_scroll(&self.scroll).flex_1().min_h_0().overflow_y_scroll().child(ui::card(cx).child(form)))
                .child(div().flex().gap_2()
                    .child(Button::new("save-schedule").primary().small().label(crate::application::i18n::text("schedules-ui.save-schedule")).disabled(v.schedule_pending).on_click(cx.listener(|this,_,_,cx| {
                        let e=&mut this.schedules_ui;let values:Vec<String>=e.fields.iter().map(|f|f.read(cx).value().to_string()).collect();
                        let minutes=if e.kind=="interval" {match values[4].parse::<u64>() {Ok(n)=>n,Err(_)=>{e.error=crate::application::i18n::text("schedules-ui.interval-must-be-a-whole-number-of").into();cx.notify();return;}}}else{60};
                        let days:Vec<u32>=if e.kind=="weekly" {match values[6].split(',').map(|s|s.trim().parse()).collect::<Result<Vec<_>,_>>() {Ok(v)=>v,Err(_)=>{e.error=crate::application::i18n::text("schedules-ui.use-comma-separated-weekdays-such-as-1").into();cx.notify();return;}}}else{vec![]};
                        this.client.schedule_action(json!({"action":"update","schedule_id":e.editing,"spec":{"title":values[0],"goal":values[1],"timezone":values[2],"kind":e.kind,"at":values[3],"every_minutes":minutes,"time":values[5],"weekdays":days,"misfire":e.misfire,"notify":e.notify,"enabled":e.enabled}}));cx.notify();
                    })))
                    .child(Button::new("cancel-schedule-edit").ghost().small().label(crate::application::i18n::text("schedules-ui.cancel")).disabled(v.schedule_pending).on_click(cx.listener(|this,_,_,cx|{this.schedules_ui.open=false;cx.notify();})))
                );
            return ui::page(cx).child(body);
        }
        if let Some(plan) = &v.schedules.selected {
            let id = plan.id.clone();
            let run_id = id.clone();
            let toggle_id = id.clone();
            let delete_id = id.clone();
            let action = if plan.spec.enabled { "pause" } else { "resume" };
            body=body.child(ui::card(cx).child(ui::section(crate::application::i18n::text("navigation.schedule-details")))
                    .child(ui::hint(rule(plan),cx)).child(ui::hint(pot_ui::machine_state::schedule_location(&plan.spec), cx)).child(ui::hint(plan.spec.goal.clone(),cx))
                    .child(ui::hint(crate::application::i18n::tr!("schedules-ui.next",if plan.next_label.is_empty(){crate::application::i18n::text("schedules-ui.not-scheduled")}else{&plan.next_label}),cx))
                    .when(!plan.last_error.is_empty(),|el|el.child(ui::hint(crate::application::i18n::tr!("schedules-ui.schedule-error",crate::application::i18n::message(&plan.last_error)),cx)))
                    .child(div().flex().gap_2()
                        .child(Button::new("schedule-run").primary().small().label(crate::application::i18n::text("schedules-ui.run-now")).disabled(v.schedule_pending).on_click(cx.listener(move|this,_,_,cx|{this.client.schedule_action(json!({"action":"run","schedule_id":run_id}));cx.notify();})))
                        .child(Button::new("schedule-pause").ghost().small().label(if plan.spec.enabled {crate::application::i18n::text("schedules-ui.paused")}else{crate::application::i18n::text("schedules-ui.resume")}).disabled(v.schedule_pending).on_click(cx.listener(move|this,_,_,cx|{this.client.schedule_action(json!({"action":action,"schedule_id":toggle_id}));cx.notify();})))
                        .child(Button::new("schedule-edit").ghost().small().label(crate::application::i18n::text("schedules-ui.edit")).disabled(v.schedule_pending).on_click(cx.listener(|this,_,window,cx|{let plan=this.client.view.lock().unwrap().schedules.selected.clone();this.schedules_ui.edit(plan.as_ref(),window,cx);cx.notify();})))
                        .child(Button::new("schedule-delete").ghost().small().text_color(cx.theme().danger).label(if self.delete_confirm {crate::application::i18n::text("schedules-ui.confirm-removal")}else{crate::application::i18n::text("schedules-ui.remove")}).disabled(v.schedule_pending).on_click(cx.listener(move|this,_,_,cx|{if this.schedules_ui.delete_confirm {this.client.schedule_action(json!({"action":"delete","schedule_id":delete_id}));this.schedules_ui.delete_confirm=false;}else{this.schedules_ui.delete_confirm=true;}cx.notify();})))
                    ).child(ui::hint(crate::application::i18n::text("schedules-ui.pausing-or-removing-only-affects-future-runs"),cx))
                )
                .child(ui::hint(crate::application::i18n::text("schedules-ui.run-history-latest-100-click-a-task"),cx))
                .child(div().id("schedule-runs").track_scroll(&self.scroll).flex_1().min_h_0().overflow_y_scroll().children(v.schedules.runs.iter().map(|run| {
                    let task=run.task_id.clone();
                    ui::card(cx).mb_2().child(ui::hint(format!("{} · {}",run.scheduled_label,state(&run.state)),cx))
                        .child(ui::hint(if matches!(run.state.as_str(), "skipped_overlap" | "skipped_missed" | "cancelled" | "interrupted") {crate::application::i18n::message(&run.summary)} else {run.summary.clone()},cx))
                        .when(task.is_some(),|el|el.child(Button::new(SharedString::from(format!("run-{}",run.id))).ghost().small().label(crate::application::i18n::text("schedules-ui.view-task")).on_click(cx.listener(move|this,_,_,cx|{this.show_tasks=false;this.client.select(task.clone().unwrap());this.first=true;cx.notify();}))))
                })))
                .child(div().flex().gap_2()
                    .child(Button::new("runs-newer").ghost().small().label(crate::application::i18n::text("schedules-ui.newer-entries")).disabled(v.runs_offset==0).on_click(cx.listener(|this,_,_,cx|{let mut v=this.client.view.lock().unwrap();v.runs_offset=v.runs_offset.saturating_sub(20);drop(v);this.client.refresh();cx.notify();})))
                    .child(Button::new("runs-older").ghost().small().label(crate::application::i18n::text("schedules-ui.older-entries")).disabled(v.schedules.runs_next_offset.is_none()).on_click(cx.listener(|this,_,_,cx|{let mut v=this.client.view.lock().unwrap();if let Some(offset)=v.schedules.runs_next_offset{v.runs_offset=offset;}drop(v);this.client.refresh();cx.notify();})))
                );
        } else {
            body = body
                .child(
                    div()
                        .id("schedule-list")
                        .track_scroll(&self.scroll)
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .when(v.schedules.items.is_empty(), |el| {
                            el.child(ui::empty(
                                gpui_kit::component::IconName::Inbox,
                                crate::application::i18n::text(
                                    "schedules-ui.no-schedules-yet-tell-pot-what-to",
                                ),
                                cx,
                            ))
                        })
                        .children(v.schedules.items.iter().map(|plan| {
                            let id = plan.id.clone();
                            ui::card(cx)
                                .mb_2()
                                .child(
                                    Button::new(SharedString::from(format!(
                                        "schedule-{}",
                                        plan.id
                                    )))
                                    .ghost()
                                    .small()
                                    .justify_start()
                                    .label(plan.spec.title.clone())
                                    .on_click(cx.listener(move |this, _, _, cx| {
                                        this.client.schedule_select(id.clone());
                                        this.schedules_ui.delete_confirm = false;
                                        cx.notify();
                                    })),
                                )
                                .child(ui::hint(rule(plan), cx))
                                .child(ui::hint(
                                    pot_ui::machine_state::schedule_location(&plan.spec),
                                    cx,
                                ))
                                .child(ui::hint(
                                    crate::application::i18n::tr!(
                                        "schedules-ui.next",
                                        if plan.next_label.is_empty() {
                                            crate::application::i18n::text(
                                                "schedules-ui.not-scheduled",
                                            )
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
                        .items_center()
                        .justify_between()
                        .child(ui::hint(
                            crate::application::i18n::tr!(
                                "schedules-ui.schedules-page",
                                v.schedules.total,
                                v.schedule_offset / 20 + 1
                            ),
                            cx,
                        ))
                        .child(
                            div()
                                .flex()
                                .gap_2()
                                .child(
                                    Button::new("schedule-prev")
                                        .ghost()
                                        .small()
                                        .label(crate::application::i18n::text(
                                            "schedules-ui.previous",
                                        ))
                                        .disabled(v.schedule_offset == 0)
                                        .on_click(cx.listener(|this, _, _, cx| {
                                            let mut v = this.client.view.lock().unwrap();
                                            v.schedule_offset =
                                                v.schedule_offset.saturating_sub(20);
                                            drop(v);
                                            this.client.refresh();
                                            cx.notify();
                                        })),
                                )
                                .child(
                                    Button::new("schedule-next")
                                        .ghost()
                                        .small()
                                        .label(crate::application::i18n::text(
                                            "schedules-ui.next-52",
                                        ))
                                        .disabled(v.schedules.next_offset.is_none())
                                        .on_click(cx.listener(|this, _, _, cx| {
                                            let mut v = this.client.view.lock().unwrap();
                                            if let Some(offset) = v.schedules.next_offset {
                                                v.schedule_offset = offset;
                                            }
                                            drop(v);
                                            this.client.refresh();
                                            cx.notify();
                                        })),
                                ),
                        ),
                );
        }
        ui::page(cx).child(body)
    }
}
fn rule(p: &ScheduleInfo) -> String {
    let frequency = match p.spec.kind.as_str() {
        "once" => crate::application::i18n::tr!("schedules-ui.once-53", p.spec.at),
        "interval" => {
            crate::application::i18n::tr!("schedules-ui.every-minutes", p.spec.every_minutes)
        }
        "weekly" => crate::application::i18n::tr!(
            "schedules-ui.weekly-on",
            p.spec
                .weekdays
                .iter()
                .filter_map(|day| [
                    crate::application::i18n::text("schedules-ui.mon"),
                    crate::application::i18n::text("schedules-ui.tue"),
                    crate::application::i18n::text("schedules-ui.wed"),
                    crate::application::i18n::text("schedules-ui.thu"),
                    crate::application::i18n::text("schedules-ui.fri"),
                    crate::application::i18n::text("schedules-ui.sat"),
                    crate::application::i18n::text("schedules-ui.sun")
                ]
                .get(day.saturating_sub(1) as usize)
                .copied())
                .collect::<Vec<_>>()
                .join(if crate::application::i18n::is_chinese() {
                    "、"
                } else {
                    ", "
                }),
            p.spec.time
        ),
        _ => crate::application::i18n::tr!("schedules-ui.daily-at", p.spec.time),
    };
    format!(
        "{} · {} · {}",
        if p.spec.enabled {
            crate::application::i18n::text("schedules-ui.enabled-64")
        } else if p.spec.kind == "once" && p.next_at_ms.is_none() {
            crate::application::i18n::text("schedules-ui.disabled-ended")
        } else {
            crate::application::i18n::text("schedules-ui.paused-66")
        },
        frequency,
        p.spec.timezone
    )
}
fn state(s: &str) -> &str {
    match s {
        "queued" => crate::application::i18n::text("schedules-ui.queued"),
        "running" => crate::application::i18n::text("schedules-ui.running"),
        "complete" => crate::application::i18n::text("schedules-ui.completed"),
        "failed" => crate::application::i18n::text("schedules-ui.failed"),
        "interrupted" => crate::application::i18n::text("schedules-ui.interrupted"),
        "cancelled" => crate::application::i18n::text("schedules-ui.cancelled"),
        "skipped_overlap" => {
            crate::application::i18n::text("schedules-ui.skipped-previous-run-unfinished")
        }
        "skipped_missed" => crate::application::i18n::text("schedules-ui.skipped-missed-run"),
        _ => s,
    }
}
