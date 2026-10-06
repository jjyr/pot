use crate::{Shell, client, gpui, ui};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, Sizable,
    button::{Button, ButtonVariants},
    chart::BarChart,
    input::{Input, InputState},
    progress::Progress,
};
use serde_json::{Value, json};

pub fn count(n: u64) -> String {
    if n >= 1_000_000 {
        format!("{:.2}M", n as f64 / 1_000_000.)
    } else if n >= 1000 {
        format!("{:.1}K", n as f64 / 1000.)
    } else {
        n.to_string()
    }
}
fn n(v: &Value, k: &str) -> u64 {
    v[k].as_u64().unwrap_or(0)
}
fn tokens(v: &Value) -> u64 {
    n(v, "input") + n(v, "output")
}
pub fn entry(v: &client::View, cx: &mut Context<Shell>) -> Button {
    Button::new("conversation-usage")
        .ghost()
        .small()
        .icon(gpui_kit::component::IconName::ChartPie)
        .text_color(cx.theme().muted_foreground)
        .label(if v.usage.is_null() {
            crate::i18n::text("navigation.usage").into()
        } else {
            format!("{} tokens", count(tokens(&v.usage["lifetime"])))
        })
        .tooltip(crate::i18n::text(
            "usage-ui.view-total-usage-for-this-conversation",
        ))
        .on_click(cx.listener(|this, _, _, cx| {
            this.usage_ui.show_all_tools = false;
            ui::command(json!({"action":"page","value":5}));
            cx.notify();
        }))
}
pub struct UsageUI {
    pub scroll: ScrollHandle,
    pub days: u32,
    pub group_offset: usize,
    pub group_context: Option<(String, u32, String)>,
    show_all_tools: bool,
    limit: Entity<InputState>,
    period: String,
    timezone: String,
    auto_resume: bool,
    editing: bool,
    error: String,
}
impl UsageUI {
    pub fn localize(&mut self, window: &mut Window, cx: &mut Context<Shell>) {
        self.limit.update(cx, |input, cx| {
            input.set_placeholder(
                crate::i18n::text("usage-ui.for-example-2m-or-500k"),
                window,
                cx,
            )
        });
    }
    pub fn open_settings(&mut self) {
        self.editing = false;
        self.error.clear();
    }
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        Self {
            scroll: ScrollHandle::new(),
            days: 7,
            group_offset: 0,
            group_context: None,
            show_all_tools: false,
            limit: cx.new(|cx| {
                InputState::new(window, cx)
                    .placeholder(crate::i18n::text("usage-ui.for-example-2m-or-500k"))
            }),
            period: "none".into(),
            timezone: "Asia/Shanghai".into(),
            auto_resume: true,
            editing: false,
            error: String::new(),
        }
    }
    pub fn render(
        &mut self,
        v: &client::View,
        page: u64,
        window: &mut Window,
        cx: &mut Context<Shell>,
    ) -> Div {
        let d = &v.usage;
        let detail = page == 5;
        let mut body = ui::content();
        if page == 6 {
            if !self.editing && !d.is_null() {
                let p = &d["quota"]["policy"];
                self.timezone = p["timezone"].as_str().unwrap_or("Asia/Shanghai").into();
                self.period = p["period"].as_str().unwrap_or("none").into();
                self.auto_resume = p["auto_resume"].as_bool().unwrap_or(true);
                self.limit.update(cx, |f, cx| {
                    f.set_value(n(p, "limit").to_string(), window, cx)
                });
                self.editing = true;
            }
            let form =
                ui::form(cx)
                    .child(
                        ui::field(crate::i18n::text("usage-ui.token-allowance")).child(
                            ui::segments(cx).children(
                                [
                                    ("none", crate::i18n::text("usage-ui.unlimited")),
                                    ("day", crate::i18n::text("schedules-ui.daily")),
                                    ("week", crate::i18n::text("schedules-ui.weekly")),
                                ]
                                .map(|(value, label)| {
                                    Button::new(SharedString::from(format!("quota-{value}")))
                                        .ghost()
                                        .small()
                                        .label(label)
                                        .when(self.period == value, |b| b.bg(cx.theme().secondary))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.usage_ui.period = value.into();
                                            cx.notify();
                                        }))
                                }),
                            ),
                        ),
                    )
                    .when(self.period != "none", |f| {
                        f.child(
                            ui::field(crate::i18n::text("usage-ui.allowance"))
                                .description(crate::i18n::text(
                                    "usage-ui.input-output-cached-tokens-are-part-of",
                                ))
                                .child(Input::new(&self.limit).aria_label(crate::i18n::text(
                                    "usage-ui.token-allowance-value",
                                ))),
                        )
                    })
                    .when(self.period != "none", |f| {
                        f.child(
                            ui::field(crate::i18n::text("usage-ui.when-allowance-resets")).child(
                                div().flex().child(
                                    Button::new("quota-auto")
                                        .ghost()
                                        .small()
                                        .label(if self.auto_resume {
                                            crate::i18n::text("usage-ui.resume-automatically")
                                        } else {
                                            crate::i18n::text("usage-ui.resume-manually")
                                        })
                                        .on_click(cx.listener(|this, _, _, cx| {
                                            this.usage_ui.auto_resume = !this.usage_ui.auto_resume;
                                            cx.notify();
                                        })),
                                ),
                            ),
                        )
                    });
            return ui::page(cx).child(body.child(ui::section(crate::i18n::text("usage-ui.periodic-allowance")))
                .child(ui::hint(crate::i18n::text("usage-ui.applies-to-the-main-chat-and-all"),cx))
                .child(ui::card(cx).child(form).when(self.period!="none",|el|el.child(ui::hint(crate::i18n::tr!("usage-ui.resets-at-00-00",if self.period=="week"{crate::i18n::text("usage-ui.monday")}else{crate::i18n::text("schedules-ui.daily")},self.timezone),cx))))
                .when(!self.error.is_empty(),|el|el.child(ui::hint(crate::i18n::message(&self.error),cx))).when(!v.usage_error.is_empty(),|el|el.child(ui::hint(crate::i18n::message(&crate::i18n::message(&v.usage_error)),cx))).when(!v.usage_notice.is_empty(),|el|el.child(ui::hint(crate::i18n::message(&v.usage_notice),cx)))
                .child(div().flex().gap_2().child(Button::new("save-quota").primary().small().label(crate::i18n::text("usage-ui.save")).disabled(v.usage_pending||d.is_null()).on_click(cx.listener(|this,_,_,cx|{
                    let u=&mut this.usage_ui;let limit=match parse_count(&u.limit.read(cx).value()){Ok(n)=>n,Err(e)=>{u.error=e.into();cx.notify();return;}};
                    let zone=u.timezone.clone();
                    this.client.usage_action("policy",json!({"period":u.period,"limit":limit,"timezone":zone,"auto_resume":u.auto_resume}));u.error.clear();cx.notify();
                }))).child(Button::new("quota-back").ghost().small().label(crate::i18n::text("usage-ui.back")).on_click(|_,_,_|ui::command(json!({"action":"page","value":4}))))));
        }
        body = body.child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .child(ui::section(if detail {
                    crate::i18n::text("usage-ui.total-usage")
                } else {
                    crate::i18n::text("usage-ui.overall-statistics")
                }))
                .when(!detail, |el| {
                    el.child(
                        Button::new("quota-settings")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("navigation.usage-limits"))
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.usage_ui.editing = false;
                                ui::command(json!({"action":"page","value":6}));
                                cx.notify();
                            })),
                    )
                }),
        );
        if detail {
            body = body.child(ui::hint(
                v.selected_task
                    .as_ref()
                    .map(|t| t.title.clone())
                    .unwrap_or(crate::i18n::text("navigation.main-chat").into()),
                cx,
            ));
        }
        if !v.usage_error.is_empty() {
            body = body.child(ui::retry(cx)).child(ui::notice(
                crate::i18n::tr!(
                    "usage-ui.unable-to-load-usage",
                    crate::i18n::message(&v.usage_error)
                ),
                true,
                cx,
            ));
        }
        if d.is_null() {
            return ui::page(cx)
                .child(body.child(ui::loading(crate::i18n::text("usage-ui.loading-usage"), cx)));
        }
        let total = if detail { &d["lifetime"] } else { &d["total"] };
        if !detail {
            body = body.child(
                ui::segments(cx).children(
                    [
                        (7, crate::i18n::text("usage-ui.last-7-days")),
                        (30, crate::i18n::text("usage-ui.last-30-days")),
                        (90, crate::i18n::text("usage-ui.last-90-days")),
                        (365, crate::i18n::text("usage-ui.last-year")),
                    ]
                    .map(|(days, label)| {
                        Button::new(("usage-days", days as usize))
                            .ghost()
                            .small()
                            .label(label)
                            .when(self.days == days, |b| b.bg(cx.theme().secondary))
                            .on_click(cx.listener(move |this, _, _, cx| {
                                this.usage_ui.days = days;
                                cx.notify();
                            }))
                    }),
                ),
            );
        }
        let mut scroll = div()
            .id("usage-scroll")
            .track_scroll(&self.scroll)
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .flex()
            .flex_col()
            .gap_5();
        scroll = scroll.child(
            ui::card(cx)
                .child(
                    div().flex().gap_6().children(
                        [
                            (
                                if detail {
                                    crate::i18n::text("usage-ui.cumulative-tokens")
                                } else {
                                    crate::i18n::text("usage-ui.total-tokens")
                                },
                                tokens(total),
                            ),
                            (crate::i18n::text("usage-ui.input"), n(total, "input")),
                            (crate::i18n::text("usage-ui.output"), n(total, "output")),
                        ]
                        .map(|(label, value)| {
                            div().flex_1().child(ui::hint(label, cx)).child(
                                div()
                                    .mt_2()
                                    .text_size(px(26.))
                                    .font_weight(FontWeight::MEDIUM)
                                    .child(count(value)),
                            )
                        }),
                    ),
                )
                .child(ui::hint(
                    crate::i18n::tr!(
                        "usage-ui.cached-input-tokens-usage-records",
                        count(n(total, "cache")),
                        n(total, "requests"),
                        if n(total, "unknown") > 0 {
                            crate::i18n::tr!(
                                "usage-ui.incomplete-records-totals-may-be-incomplete",
                                n(total, "unknown")
                            )
                        } else {
                            String::new()
                        }
                    ),
                    cx,
                )),
        );
        let routing = &d["routing_total"];
        if n(routing, "requests") > 0 {
            scroll = scroll.child(
                ui::card(cx)
                    .child(ui::section(crate::i18n::text(
                        "usage-ui.main-chat-usage-including-task-routing",
                    )))
                    .child(div().mt_2().text_size(px(22.)).child(
                        if tokens(routing) == 0 && n(routing, "unknown") > 0 {
                            crate::i18n::text("usage-ui.usage-is-not-fully-reported-yet").into()
                        } else {
                            format!("{} tokens", count(tokens(routing)))
                        },
                    ))
                    .child(ui::hint(
                        crate::i18n::tr!(
                            "usage-ui.input-output-cached-input",
                            count(n(routing, "input")),
                            count(n(routing, "output")),
                            count(n(routing, "cache")),
                            if n(routing, "unknown") > 0 {
                                crate::i18n::tr!(
                                    "usage-ui.incomplete-usage-records",
                                    n(routing, "unknown")
                                )
                            } else {
                                String::new()
                            }
                        ),
                        cx,
                    ))
                    .child(ui::hint(
                        crate::i18n::text("usage-ui.includes-the-entire-main-chat-turn-that"),
                        cx,
                    )),
            );
        }
        let groups = d["agent_models"].as_array().cloned().unwrap_or_default();
        if !groups.is_empty() || self.group_offset > 0 {
            let mut card =
                ui::card(cx).child(ui::section(crate::i18n::text("usage-ui.agent-model-usage")));
            for (index, row) in groups.iter().enumerate() {
                let agent = match row["agent"].as_str().unwrap_or("unknown") {
                    "pot" => "Pot",
                    "codex" => "Codex",
                    "claude" => "Claude",
                    _ => crate::i18n::text("usage-ui.unknown-agent"),
                };
                let tooltip = crate::i18n::tr!(
                    "usage-ui.records-incomplete-records",
                    n(row, "requests"),
                    n(row, "unknown")
                );
                card = card.child(
                    div()
                        .id(("agent-model-usage", index))
                        .mt_3()
                        .flex()
                        .flex_col()
                        .gap_1()
                        .child(
                            div()
                                .flex()
                                .gap_3()
                                .child(div().flex_1().min_w_0().truncate().child(format!(
                                        "{} · {}",
                                        agent,
                                        row["model"]
                                            .as_str()
                                            .unwrap_or(crate::i18n::text("usage-ui.unknown-model"))
                                    )))
                                .child(div().child(format!("{} tokens", count(tokens(row))))),
                        )
                        .child(ui::hint(
                            crate::i18n::tr!(
                                "usage-ui.input-output-cached-input",
                                count(n(row, "input")),
                                count(n(row, "output")),
                                count(n(row, "cache")),
                                if n(row, "unknown") > 0 {
                                    crate::i18n::tr!(
                                        "usage-ui.incomplete-usage-records",
                                        n(row, "unknown")
                                    )
                                } else {
                                    String::new()
                                }
                            ),
                            cx,
                        ))
                        .tooltip(move |window, cx| ui::title_tooltip(tooltip.clone(), window, cx)),
                );
            }
            let next = d["agent_model_next_offset"].as_u64().map(|v| v as usize);
            if self.group_offset > 0 || next.is_some() {
                card = card.child(
                    div()
                        .mt_3()
                        .flex()
                        .gap_2()
                        .child(
                            Button::new("agent-model-prev")
                                .ghost()
                                .small()
                                .label(crate::i18n::text("schedules-ui.previous"))
                                .disabled(self.group_offset == 0)
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.usage_ui.group_offset =
                                        this.usage_ui.group_offset.saturating_sub(20);
                                    cx.notify();
                                })),
                        )
                        .child(
                            Button::new("agent-model-next")
                                .ghost()
                                .small()
                                .label(crate::i18n::text("schedules-ui.next-52"))
                                .disabled(next.is_none())
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    if let Some(offset) = next {
                                        this.usage_ui.group_offset = offset;
                                        cx.notify();
                                    }
                                })),
                        ),
                );
            }
            scroll = scroll.child(card);
        }
        if d["native_turn_usage"] == true {
            scroll = scroll.child(ui::hint(
                crate::i18n::text("usage-ui.native-cli-usage-is-recorded-by-turn"),
                cx,
            ));
        }
        if detail {
            let compression = &d["compaction_total"];
            let total_tokens = tokens(total);
            let compressed = tokens(compression).min(total_tokens);
            let conversation = total_tokens.saturating_sub(compressed);
            let primary = cx.theme().chart_1;
            let secondary = cx.theme().muted_foreground.opacity(0.65);
            let mut model =
                ui::card(cx).child(ui::section(crate::i18n::text("usage-ui.model-consumption")));
            if total_tokens > 0 {
                model = model.child(
                    div()
                        .flex()
                        .w_full()
                        .h(px(12.))
                        .rounded_full()
                        .overflow_hidden()
                        .when(conversation > 0, |el| {
                            el.child(
                                div()
                                    .h_full()
                                    .w(relative(conversation as f32 / total_tokens as f32))
                                    .rounded_l_full()
                                    .when(compressed == 0, |el| el.rounded_r_full())
                                    .bg(primary),
                            )
                        })
                        .when(compressed > 0, |el| {
                            el.child(
                                div()
                                    .h_full()
                                    .w(relative(compressed as f32 / total_tokens as f32))
                                    .rounded_r_full()
                                    .when(conversation == 0, |el| el.rounded_l_full())
                                    .bg(secondary),
                            )
                        }),
                );
                for (id, label, amount, color, input, output, cache) in [
                    (
                        "conversation",
                        crate::i18n::text("navigation.task-conversation"),
                        conversation,
                        primary,
                        n(total, "input").saturating_sub(n(compression, "input")),
                        n(total, "output").saturating_sub(n(compression, "output")),
                        n(total, "cache").saturating_sub(n(compression, "cache")),
                    ),
                    (
                        "compaction",
                        crate::i18n::text("usage-ui.context-compaction"),
                        compressed,
                        secondary,
                        n(compression, "input"),
                        n(compression, "output"),
                        n(compression, "cache"),
                    ),
                ] {
                    if amount == 0 {
                        continue;
                    }
                    let tooltip = crate::i18n::tr!(
                        "usage-ui.input-output-cached-input-43",
                        count(input),
                        count(output),
                        count(cache)
                    );
                    model = model.child(
                        div()
                            .id(id)
                            .flex()
                            .items_center()
                            .gap_2()
                            .child(div().size(px(8.)).rounded_full().bg(color))
                            .child(ui::hint(
                                format!(
                                    "{label}  {} · {:.1}%",
                                    count(amount),
                                    amount as f64 / total_tokens as f64 * 100.
                                ),
                                cx,
                            ))
                            .tooltip(move |window, cx| {
                                ui::title_tooltip(tooltip.clone(), window, cx)
                            }),
                    );
                }
            } else {
                model = model.child(ui::hint(
                    crate::i18n::text("usage-ui.no-confirmed-model-usage-yet"),
                    cx,
                ));
            }
            scroll = scroll.child(model);
            let mut tools = d["tools"].as_array().cloned().unwrap_or_default();
            tools.sort_by(|a, b| {
                n(b, "bytes")
                    .cmp(&n(a, "bytes"))
                    .then_with(|| a["name"].as_str().cmp(&b["name"].as_str()))
            });
            if !tools.is_empty() {
                let sum: u64 = tools.iter().map(|r| n(r, "bytes")).sum();
                let max = tools
                    .iter()
                    .map(|r| n(r, "bytes"))
                    .max()
                    .unwrap_or(0)
                    .max(1);
                let mut card = ui::card(cx)
                    .child(
                        div()
                            .flex()
                            .justify_between()
                            .child(div().child(crate::i18n::text("usage-ui.tool-output")))
                            .child(ui::hint(
                                crate::i18n::tr!("usage-ui.about-tokens", count(sum.div_ceil(4))),
                                cx,
                            )),
                    )
                    .child(ui::hint(
                        crate::i18n::text("usage-ui.estimated-from-utf-8-output-bytes-4"),
                        cx,
                    ));
                for (index, r) in tools
                    .iter()
                    .take(if self.show_all_tools { tools.len() } else { 5 })
                    .enumerate()
                {
                    let name = r["name"].as_str().unwrap_or("tool").to_string();
                    let tooltip = crate::i18n::tr!(
                        "usage-ui.failed-1-seconds-unfinished",
                        name,
                        n(r, "failed"),
                        n(r, "duration_ms") as f64 / 1000.,
                        n(r, "unfinished")
                    );
                    card = card.child(
                        div()
                            .id(("tool-output", index))
                            .flex()
                            .flex_col()
                            .gap_2()
                            .child(
                                div()
                                    .flex()
                                    .items_center()
                                    .gap_3()
                                    .child(div().flex_1().min_w_0().truncate().child(name))
                                    .child(ui::hint(
                                        crate::i18n::tr!(
                                            "usage-ui.about-tokens-calls",
                                            count(n(r, "bytes").div_ceil(4)),
                                            n(r, "calls")
                                        ),
                                        cx,
                                    )),
                            )
                            .child(
                                div()
                                    .h(px(6.))
                                    .w_full()
                                    .rounded_full()
                                    .bg(cx.theme().secondary)
                                    .overflow_hidden()
                                    .child(
                                        div()
                                            .h_full()
                                            .w(relative(n(r, "bytes") as f32 / max as f32))
                                            .rounded_full()
                                            .bg(primary),
                                    ),
                            )
                            .tooltip(move |window, cx| {
                                ui::title_tooltip(tooltip.clone(), window, cx)
                            }),
                    );
                }
                if tools.len() > 5 {
                    card = card.child(
                        Button::new("usage-more-tools")
                            .ghost()
                            .small()
                            .label(if self.show_all_tools {
                                crate::i18n::text("usage-ui.show-less").into()
                            } else {
                                crate::i18n::tr!("usage-ui.show-all-tools", tools.len())
                            })
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.usage_ui.show_all_tools = !this.usage_ui.show_all_tools;
                                cx.notify();
                            })),
                    );
                }
                scroll = scroll.child(card);
            }
        } else {
            let data = d["buckets"].as_array().cloned().unwrap_or_default();
            let color = cx.theme().chart_1;
            scroll = scroll.child(
                ui::card(cx)
                    .child(ui::hint(
                        crate::i18n::tr!("usage-ui.today-is-still-in-progress",
                            if self.days == 365 {
                                crate::i18n::text("usage-ui.up-to-7-days-per-bar")
                            } else {
                                crate::i18n::text("usage-ui.one-day-per-bar")
                            },
                            d["timezone"].as_str().unwrap_or("")
                        ),
                        cx,
                    ))
                    .child(
                        div()
                            .h(px(240.))
                            .w_full()
                            .when(tokens(total) == 0, |el| {
                                el.flex().items_center().justify_center().child(ui::hint(
                                    if n(total, "requests") == 0 {
                                        crate::i18n::text("usage-ui.no-usage-records-in-the-selected-range")
                                    } else {
                                        crate::i18n::text("usage-ui.no-confirmed-token-usage-yet")
                                    },
                                    cx,
                                ))
                            })
                            .when(tokens(total) > 0, |el| {
                                el.child(
                                    BarChart::new(data)
                                        .id("usage-chart")
                                        .band(|d: &Value| {
                                            d["label"].as_str().unwrap_or("").to_string()
                                        })
                                        .value(|d: &Value| tokens(d) as f64)
                                        .fill(move |_, _, _, _| color)
                                        .value_axis(true)
                                        .value_tick_format(|n| count(n.max(0.) as u64))
                                        .band_tick_count(7)
                                        .tooltip_title(|d| {
                                            d["range"].as_str().unwrap_or("").to_string().into()
                                        })
                                        .tooltip_content(|d, _, cx| {
                                            ui::card(cx)
                                                .child(div().child(
                                                    d["range"].as_str().unwrap_or("").to_string(),
                                                ))
                                                .child(ui::hint(
                                                    crate::i18n::tr!("usage-ui.total-input-output",
                                                        count(tokens(d)),
                                                        count(n(d, "input")),
                                                        count(n(d, "output"))
                                                    ),
                                                    cx,
                                                ))
                                                .child(ui::hint(
                                                    crate::i18n::tr!("usage-ui.cached-input-unknown-usage-records",
                                                        count(n(d, "cache")),
                                                        n(d, "unknown")
                                                    ),
                                                    cx,
                                                ))
                                        }),
                                )
                            }),
                    ),
            );
            let q = &d["quota"];
            if q["active"] == true {
                scroll = scroll.child(
                    ui::card(cx)
                        .child(div().child(crate::i18n::text("usage-ui.pot-usage-limits")))
                        .child(ui::hint(
                            crate::i18n::tr!(
                                "usage-ui.tokens-reserved",
                                if q["policy"]["period"] == "week" {
                                    crate::i18n::text("usage-ui.this-week")
                                } else {
                                    crate::i18n::text("usage-ui.today")
                                },
                                count(n(q, "used")),
                                count(n(&q["policy"], "limit")),
                                count(n(q, "reserved"))
                            ),
                            cx,
                        ))
                        .child(
                            Progress::new("quota-progress").value(
                                (n(q, "used") as f64 / n(&q["policy"], "limit").max(1) as f64
                                    * 100.)
                                    .min(100.) as f32,
                            ),
                        )
                        .child(ui::hint(
                            crate::i18n::tr!(
                                "usage-ui.resets",
                                q["reset_label"].as_str().unwrap_or("")
                            ),
                            cx,
                        )),
                );
            }
        }
        if d["waiting"].is_object() {
            scroll = scroll.child(
                ui::card(cx)
                    .child(ui::hint(
                        crate::i18n::text(
                            "usage-ui.this-conversation-is-waiting-for-pot-allowance",
                        ),
                        cx,
                    ))
                    .child(
                        Button::new("usage-resume")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("usage-ui.continue"))
                            .disabled(v.usage_pending)
                            .on_click(cx.listener(|this, _, _, _| {
                                let scope = this.client.view.lock().unwrap().usage["scope"].clone();
                                this.client.usage_action("resume", json!({"scope":scope}));
                            })),
                    ),
            );
        }
        if d["waiting"].is_object() {
            scroll = scroll.child(
                Button::new("cancel-usage-wait")
                    .ghost()
                    .small()
                    .label(crate::i18n::text("usage-ui.cancel-wait"))
                    .disabled(v.usage_pending)
                    .on_click(cx.listener(|this, _, _, _| {
                        if this.client.view.lock().unwrap().selected.is_empty() {
                            this.client.usage_action("cancel", json!({}));
                        } else {
                            this.client.cancel();
                        }
                    })),
            );
        }
        if d["waiting"].is_object()
            && d["quota"]["active"] == true
            && n(&d["waiting"], "required") > n(&d["quota"]["policy"], "limit")
        {
            scroll = scroll.child(ui::hint(
                crate::i18n::text("usage-ui.this-allowance-is-smaller-than-the-context"),
                cx,
            ));
        }
        body = body.child(scroll);
        ui::page(cx).child(body)
    }
}
fn parse_count(text: &str) -> Result<u64, &'static str> {
    let s = text.trim().to_ascii_uppercase();
    let (raw, factor) = if let Some(s) = s.strip_suffix('K') {
        (s, 1_000u64)
    } else if let Some(s) = s.strip_suffix('M') {
        (s, 1_000_000)
    } else {
        (s.as_str(), 1)
    };
    let parts: Vec<_> = raw.split('.').collect();
    if parts.is_empty()
        || parts.len() > 2
        || parts
            .iter()
            .any(|p| p.is_empty() || !p.bytes().all(|b| b.is_ascii_digit()))
    {
        return Err(crate::i18n::text(
            "usage-ui.enter-a-positive-integer-or-a-k",
        ));
    }
    let a = parts[0]
        .parse::<u64>()
        .ok()
        .and_then(|n| n.checked_mul(factor))
        .ok_or(crate::i18n::text("usage-ui.value-is-too-large"))?;
    let b = if parts.len() == 2 {
        let scale = 10u64
            .checked_pow(parts[1].len() as u32)
            .ok_or(crate::i18n::text("usage-ui.too-many-decimal-places"))?;
        let n = parts[1]
            .parse::<u64>()
            .ok()
            .and_then(|n| n.checked_mul(factor))
            .ok_or(crate::i18n::text("usage-ui.value-is-too-large"))?;
        if n % scale != 0 {
            return Err(crate::i18n::text(
                "usage-ui.token-count-must-be-a-whole-number",
            ));
        }
        n / scale
    } else {
        0
    };
    let n = a
        .checked_add(b)
        .ok_or(crate::i18n::text("usage-ui.value-is-too-large"))?;
    if !(1..=9_007_199_254_740_991).contains(&n) {
        return Err(crate::i18n::text("usage-ui.value-is-out-of-range"));
    }
    Ok(n)
}
#[cfg(test)]
mod tests {
    use super::parse_count;
    #[test]
    fn limits() {
        assert_eq!(parse_count("1.5M"), Ok(1500000));
        assert_eq!(parse_count("500K"), Ok(500000));
        for s in ["0", "NaN", "1.1", "-1", "1e6", "9007199254740992"] {
            assert!(parse_count(s).is_err(), "{s}");
        }
    }
}
