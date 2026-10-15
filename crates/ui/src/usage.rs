use crate::pages::tr;
use crate::{
    localization::Catalog,
    pages::{self, Handler, Layout, PageAction},
    usage_state::count,
};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, Sizable,
    button::{Button, ButtonVariants},
    chart::BarChart,
    input::{Input, InputState},
    progress::Progress,
};
use serde_json::{Value, json};
fn n(v: &Value, k: &str) -> u64 {
    v[k].as_u64().unwrap_or(0)
}
fn tokens(v: &Value) -> u64 {
    n(v, "input") + n(v, "output")
}
pub struct UsageView<'a> {
    pub page: u64,
    pub usage: &'a Value,
    pub usage_error: &'a str,
    pub usage_notice: &'a str,
    pub usage_pending: bool,
    pub loading: bool,
    pub selected_title: Option<&'a str>,
    pub controls: bool,
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
    pub fn localize(&mut self, catalog: Catalog, window: &mut Window, cx: &mut App) {
        self.limit.update(cx, |input, cx| {
            input.set_placeholder(catalog.text("usage-ui.for-example-2m-or-500k"), window, cx)
        });
    }
    pub fn open_settings(&mut self) {
        self.editing = false;
        self.error.clear();
    }
    pub fn new(catalog: Catalog, window: &mut Window, cx: &mut App) -> Self {
        Self {
            scroll: ScrollHandle::new(),
            days: 7,
            group_offset: 0,
            group_context: None,
            show_all_tools: false,
            limit: cx.new(|cx| {
                InputState::new(window, cx)
                    .placeholder(catalog.text("usage-ui.for-example-2m-or-500k"))
            }),
            period: "none".into(),
            timezone: "Asia/Shanghai".into(),
            auto_resume: true,
            editing: false,
            error: String::new(),
        }
    }
    pub fn apply(&mut self, event: &PageAction, catalog: Catalog, cx: &App) -> Option<PageAction> {
        match event.kind {
            "usage-period" => self.period = event.value.as_str().unwrap_or("none").into(),
            "usage-auto" => self.auto_resume = !self.auto_resume,
            "usage-days" => {
                self.days = event.value.as_u64().unwrap_or(7) as u32;
                self.group_offset = 0;
            }
            "usage-group-prev" => self.group_offset = self.group_offset.saturating_sub(20),
            "usage-group-next" => {
                if let Some(offset) = event.value.as_u64() {
                    self.group_offset = offset as usize;
                }
            }
            "usage-tools" => self.show_all_tools = !self.show_all_tools,
            "usage-page" => {
                self.editing = false;
                self.show_all_tools = false;
                return Some(event.clone());
            }
            "usage-save" => {
                let text = self.limit.read(cx).value();
                match crate::usage_state::parse_count(&text) {
                    Ok(limit) => {
                        self.error.clear();
                        let value = json!({"period":self.period,"limit":limit,"timezone":self.timezone,"auto_resume":self.auto_resume});
                        return Some(PageAction {
                            kind: "usage-policy",
                            value,
                        });
                    }
                    Err(key) => self.error = catalog.text(key).into(),
                }
            }
            _ => return Some(event.clone()),
        }
        None
    }
    pub fn render(
        &mut self,
        v: UsageView<'_>,
        layout: Layout,
        catalog: Catalog,
        handler: Handler,
        window: &mut Window,
        cx: &mut App,
    ) -> Div {
        let page = v.page;
        let d = v.usage;
        let detail = page == 5;
        let mut body = pages::content(layout);
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
            let form = pages::form(cx)
                .child(
                    pages::field(catalog.text("usage-ui.token-allowance")).child(
                        pages::segments(cx).children(
                            [
                                ("none", catalog.text("usage-ui.unlimited")),
                                ("day", catalog.text("schedules-ui.daily")),
                                ("week", catalog.text("schedules-ui.weekly")),
                            ]
                            .map(|(value, label)| {
                                Button::new(SharedString::from(format!("quota-{value}")))
                                    .ghost()
                                    .when(layout.compact, |b| b.min_h(px(44.)))
                                    .small()
                                    .label(label)
                                    .when(self.period == value, |b| {
                                        b.bg(crate::design::surface(cx)).shadow_sm()
                                    })
                                    .on_click(pages::on(&handler, "usage-period", json!(value)))
                            }),
                        ),
                    ),
                )
                .when(self.period != "none", |f| {
                    f.child(
                        pages::field(catalog.text("usage-ui.allowance"))
                            .description(
                                catalog.text("usage-ui.input-output-cached-tokens-are-part-of"),
                            )
                            .child(
                                Input::new(&self.limit)
                                    .aria_label(catalog.text("usage-ui.token-allowance-value")),
                            ),
                    )
                })
                .when(self.period != "none", |f| {
                    f.child(
                        pages::field(catalog.text("usage-ui.when-allowance-resets")).child(
                            div().flex().child(
                                Button::new("quota-auto")
                                    .ghost()
                                    .when(layout.compact, |b| b.min_h(px(44.)))
                                    .small()
                                    .label(if self.auto_resume {
                                        catalog.text("usage-ui.resume-automatically")
                                    } else {
                                        catalog.text("usage-ui.resume-manually")
                                    })
                                    .on_click(pages::on(&handler, "usage-auto", Value::Null)),
                            ),
                        ),
                    )
                });
            return pages::page(layout, cx).child(
                body.child(pages::section(catalog.text("usage-ui.periodic-allowance")))
                    .child(pages::hint(
                        catalog.text("usage-ui.applies-to-the-main-chat-and-all"),
                        cx,
                    ))
                    .child(
                        pages::card(cx)
                            .child(form)
                            .when(self.period != "none", |el| {
                                el.child(pages::hint(
                                    tr!(
                                        catalog,
                                        "usage-ui.resets-at-00-00",
                                        if self.period == "week" {
                                            catalog.text("usage-ui.monday")
                                        } else {
                                            catalog.text("schedules-ui.daily")
                                        },
                                        self.timezone
                                    ),
                                    cx,
                                ))
                            }),
                    )
                    .when(!self.error.is_empty(), |el| {
                        el.child(pages::hint(catalog.message(&self.error), cx))
                    })
                    .when(!v.usage_error.is_empty(), |el| {
                        el.child(pages::hint(
                            catalog.message(&catalog.message(v.usage_error)),
                            cx,
                        ))
                    })
                    .when(!v.usage_notice.is_empty(), |el| {
                        el.child(pages::hint(catalog.message(v.usage_notice), cx))
                    })
                    .child(
                        div()
                            .flex()
                            .gap_2()
                            .child(
                                Button::new("save-quota")
                                    .primary()
                                    .small()
                                    .label(catalog.text("usage-ui.save"))
                                    .disabled(v.usage_pending || d.is_null() || !v.controls)
                                    .on_click(pages::on(&handler, "usage-save", Value::Null)),
                            )
                            .when(layout.compact, |el| {
                                el.child(
                                    Button::new("quota-back")
                                        .ghost()
                                        .small()
                                        .label(catalog.text("usage-ui.back"))
                                        .on_click(pages::on(&handler, "usage-page", json!(4))),
                                )
                            }),
                    ),
            );
        }
        body = body.child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .child(pages::section(if detail {
                    catalog.text("usage-ui.total-usage")
                } else {
                    catalog.text("usage-ui.overall-statistics")
                }))
                .when(!detail, |el| {
                    el.child(
                        Button::new("quota-settings")
                            .ghost()
                            .when(layout.compact, |b| b.min_h(px(44.)))
                            .small()
                            .label(catalog.text("navigation.usage-limits"))
                            .disabled(!v.controls)
                            .on_click(pages::on(&handler, "usage-page", json!(6))),
                    )
                }),
        );
        if detail {
            body = body.child(pages::hint(
                v.selected_title
                    .map(str::to_owned)
                    .unwrap_or(catalog.text("navigation.main-chat").into()),
                cx,
            ));
        }
        if !v.usage_error.is_empty() {
            body = body
                .child(pages::retry(&handler, catalog))
                .child(pages::notice(
                    tr!(
                        catalog,
                        "usage-ui.unable-to-load-usage",
                        catalog.message(v.usage_error)
                    ),
                    true,
                    cx,
                ));
        }
        let total = if detail { &d["lifetime"] } else { &d["total"] };
        if !detail {
            body = body.child(
                pages::segments(cx).children(
                    [
                        (7, catalog.text("usage-ui.last-7-days")),
                        (30, catalog.text("usage-ui.last-30-days")),
                        (90, catalog.text("usage-ui.last-90-days")),
                        (365, catalog.text("usage-ui.last-year")),
                    ]
                    .map(|(days, label)| {
                        Button::new(("usage-days", days as usize))
                            .ghost()
                            .when(layout.compact, |b| b.min_h(px(44.)))
                            .small()
                            .label(label)
                            .when(self.days == days, |b| {
                                b.bg(crate::design::surface(cx)).shadow_sm()
                            })
                            .on_click(pages::on(&handler, "usage-days", json!(days)))
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
            pages::card(cx)
                .relative()
                .when(v.loading, |el| {
                    el.child(
                        div()
                            .absolute()
                            .top_4()
                            .right_4()
                            .child(gpui_kit::component::spinner::Spinner::new().small()),
                    )
                })
                .child(
                    div()
                        .flex()
                        .gap_6()
                        .when(layout.compact, |el| el.flex_wrap())
                        .children(
                            [
                                (
                                    if detail {
                                        catalog.text("usage-ui.cumulative-tokens")
                                    } else {
                                        catalog.text("usage-ui.total-tokens")
                                    },
                                    tokens(total),
                                ),
                                (catalog.text("usage-ui.input"), n(total, "input")),
                                (catalog.text("usage-ui.output"), n(total, "output")),
                            ]
                            .map(|(label, value)| {
                                div().flex_1().child(pages::hint(label, cx)).child(
                                    div()
                                        .mt_2()
                                        .text_size(px(26.))
                                        .font_weight(FontWeight::MEDIUM)
                                        .child(count(value)),
                                )
                            }),
                        ),
                )
                .child(pages::hint(
                    tr!(
                        catalog,
                        "usage-ui.cached-input-tokens-usage-records",
                        count(n(total, "cache")),
                        n(total, "requests"),
                        if n(total, "unknown") > 0 {
                            tr!(
                                catalog,
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
                pages::card(cx)
                    .child(pages::section(
                        catalog.text("usage-ui.main-chat-usage-including-task-routing"),
                    ))
                    .child(div().mt_2().text_size(px(22.)).child(
                        if tokens(routing) == 0 && n(routing, "unknown") > 0 {
                            catalog
                                .text("usage-ui.usage-is-not-fully-reported-yet")
                                .into()
                        } else {
                            format!("{} tokens", count(tokens(routing)))
                        },
                    ))
                    .child(pages::hint(
                        tr!(
                            catalog,
                            "usage-ui.input-output-cached-input",
                            count(n(routing, "input")),
                            count(n(routing, "output")),
                            count(n(routing, "cache")),
                            if n(routing, "unknown") > 0 {
                                tr!(
                                    catalog,
                                    "usage-ui.incomplete-usage-records",
                                    n(routing, "unknown")
                                )
                            } else {
                                String::new()
                            }
                        ),
                        cx,
                    ))
                    .child(pages::hint(
                        catalog.text("usage-ui.includes-the-entire-main-chat-turn-that"),
                        cx,
                    )),
            );
        }
        let groups = d["agent_models"].as_array().cloned().unwrap_or_default();
        if !groups.is_empty() || self.group_offset > 0 {
            let mut card =
                pages::card(cx).child(pages::section(catalog.text("usage-ui.agent-model-usage")));
            for (index, row) in groups.iter().enumerate() {
                let agent = match row["agent"].as_str().unwrap_or("unknown") {
                    "pot" => "Pot",
                    "claude" => "Claude",
                    _ => catalog.text("usage-ui.unknown-agent"),
                };
                let tooltip = tr!(
                    catalog,
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
                                            .unwrap_or(catalog.text("usage-ui.unknown-model"))
                                    )))
                                .child(div().child(format!("{} tokens", count(tokens(row))))),
                        )
                        .child(pages::hint(
                            tr!(
                                catalog,
                                "usage-ui.input-output-cached-input",
                                count(n(row, "input")),
                                count(n(row, "output")),
                                count(n(row, "cache")),
                                if n(row, "unknown") > 0 {
                                    tr!(
                                        catalog,
                                        "usage-ui.incomplete-usage-records",
                                        n(row, "unknown")
                                    )
                                } else {
                                    String::new()
                                }
                            ),
                            cx,
                        ))
                        .tooltip(move |window, cx| {
                            pages::title_tooltip(tooltip.clone(), window, cx)
                        }),
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
                                .when(layout.compact, |b| b.min_h(px(44.)))
                                .small()
                                .label(catalog.text("schedules-ui.previous"))
                                .disabled(self.group_offset == 0)
                                .on_click(pages::on(&handler, "usage-group-prev", Value::Null)),
                        )
                        .child(
                            Button::new("agent-model-next")
                                .ghost()
                                .when(layout.compact, |b| b.min_h(px(44.)))
                                .small()
                                .label(catalog.text("schedules-ui.next-52"))
                                .disabled(next.is_none())
                                .on_click(pages::on(&handler, "usage-group-next", json!(next))),
                        ),
                );
            }
            scroll = scroll.child(card);
        }
        if detail {
            let compression = &d["compaction_total"];
            let total_tokens = tokens(total);
            let compressed = tokens(compression).min(total_tokens);
            let conversation = total_tokens.saturating_sub(compressed);
            let primary = cx.theme().chart_1;
            let secondary = cx.theme().muted_foreground.opacity(0.65);
            let mut model =
                pages::card(cx).child(pages::section(catalog.text("usage-ui.model-consumption")));
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
                        catalog.text("navigation.task-conversation"),
                        conversation,
                        primary,
                        n(total, "input").saturating_sub(n(compression, "input")),
                        n(total, "output").saturating_sub(n(compression, "output")),
                        n(total, "cache").saturating_sub(n(compression, "cache")),
                    ),
                    (
                        "compaction",
                        catalog.text("usage-ui.context-compaction"),
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
                    let tooltip = tr!(
                        catalog,
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
                            .child(pages::hint(
                                format!(
                                    "{label}  {} · {:.1}%",
                                    count(amount),
                                    amount as f64 / total_tokens as f64 * 100.
                                ),
                                cx,
                            ))
                            .tooltip(move |window, cx| {
                                pages::title_tooltip(tooltip.clone(), window, cx)
                            }),
                    );
                }
            } else {
                model = model.child(pages::hint(
                    catalog.text("usage-ui.no-confirmed-model-usage-yet"),
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
                let mut card = pages::card(cx)
                    .child(
                        div()
                            .flex()
                            .justify_between()
                            .child(div().child(catalog.text("usage-ui.tool-output")))
                            .child(pages::hint(
                                tr!(catalog, "usage-ui.about-tokens", count(sum.div_ceil(4))),
                                cx,
                            )),
                    )
                    .child(pages::hint(
                        catalog.text("usage-ui.estimated-from-utf-8-output-bytes-4"),
                        cx,
                    ));
                for (index, r) in tools
                    .iter()
                    .take(if self.show_all_tools { tools.len() } else { 5 })
                    .enumerate()
                {
                    let name = r["name"].as_str().unwrap_or("tool").to_string();
                    let tooltip = tr!(
                        catalog,
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
                                    .child(pages::hint(
                                        tr!(
                                            catalog,
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
                                pages::title_tooltip(tooltip.clone(), window, cx)
                            }),
                    );
                }
                if tools.len() > 5 {
                    card = card.child(
                        Button::new("usage-more-tools")
                            .ghost()
                            .when(layout.compact, |b| b.min_h(px(44.)))
                            .small()
                            .label(if self.show_all_tools {
                                catalog.text("usage-ui.show-less").into()
                            } else {
                                tr!(catalog, "usage-ui.show-all-tools", tools.len())
                            })
                            .on_click(pages::on(&handler, "usage-tools", Value::Null)),
                    );
                }
                scroll = scroll.child(card);
            }
        } else {
            let data = d["buckets"].as_array().cloned().unwrap_or_default();
            let color = cx.theme().chart_1;
            scroll = scroll.child(
                pages::card(cx)
                    .child(pages::hint(
                        tr!(catalog,"usage-ui.today-is-still-in-progress",
                            if self.days == 365 {
                                catalog.text("usage-ui.up-to-7-days-per-bar")
                            } else {
                                catalog.text("usage-ui.one-day-per-bar")
                            },
                            d["timezone"].as_str().unwrap_or("")
                        ),
                        cx,
                    ))
                    .child(
                        div()
                            .h(px(if tokens(total) == 0 { 152. } else { 240. }))
                            .w_full()
                            .when(tokens(total) == 0, |el| {
                                el.flex().items_center().justify_center().child(pages::hint(
                                    if v.loading {
                                        catalog.text("usage-ui.loading-usage")
                                    } else if n(total, "requests") == 0 {
                                        catalog.text("usage-ui.no-usage-records-in-the-selected-range")
                                    } else {
                                        catalog.text("usage-ui.no-confirmed-token-usage-yet")
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
                                        .tooltip_content(move |d, _, cx| {
                                            pages::card(cx)
                                                .child(div().child(
                                                    d["range"].as_str().unwrap_or("").to_string(),
                                                ))
                                                .child(pages::hint(
                                                    tr!(catalog,"usage-ui.total-input-output",
                                                        count(tokens(d)),
                                                        count(n(d, "input")),
                                                        count(n(d, "output"))
                                                    ),
                                                    cx,
                                                ))
                                                .child(pages::hint(
                                                    tr!(catalog,"usage-ui.cached-input-unknown-usage-records",
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
                    pages::card(cx)
                        .child(div().child(catalog.text("usage-ui.pot-usage-limits")))
                        .child(pages::hint(
                            tr!(
                                catalog,
                                "usage-ui.tokens-reserved",
                                if q["policy"]["period"] == "week" {
                                    catalog.text("usage-ui.this-week")
                                } else {
                                    catalog.text("usage-ui.today")
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
                        .child(pages::hint(
                            tr!(
                                catalog,
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
                pages::card(cx)
                    .child(pages::hint(
                        catalog.text("usage-ui.this-conversation-is-waiting-for-pot-allowance"),
                        cx,
                    ))
                    .child(
                        Button::new("usage-resume")
                            .ghost()
                            .when(layout.compact, |b| b.min_h(px(44.)))
                            .small()
                            .label(catalog.text("usage-ui.continue"))
                            .disabled(v.usage_pending || !v.controls)
                            .on_click(pages::on(
                                &handler,
                                "usage-resume",
                                json!({"scope":d["scope"]}),
                            )),
                    ),
            );
        }
        if d["waiting"].is_object() {
            scroll = scroll.child(
                Button::new("cancel-usage-wait")
                    .ghost()
                    .when(layout.compact, |b| b.min_h(px(44.)))
                    .small()
                    .label(catalog.text("usage-ui.cancel-wait"))
                    .disabled(v.usage_pending || !v.controls)
                    .on_click(pages::on(&handler, "usage-cancel", Value::Null)),
            );
        }
        if d["waiting"].is_object()
            && d["quota"]["active"] == true
            && n(&d["waiting"], "required") > n(&d["quota"]["policy"], "limit")
        {
            scroll = scroll.child(pages::hint(
                catalog.text("usage-ui.this-allowance-is-smaller-than-the-context"),
                cx,
            ));
        }
        body = body.child(scroll);
        pages::page(layout, cx).child(body)
    }
}
