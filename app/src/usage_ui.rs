use crate::{Shell, client, gpui, ui};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, Sizable,
    button::{Button, ButtonVariants},
    chart::BarChart,
    form::{Field, Form},
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
            "用量".into()
        } else {
            format!("{} tokens", count(tokens(&v.usage["lifetime"])))
        })
        .tooltip("查看此会话的累计用量")
        .on_click(cx.listener(|this, _, _, cx| {
            this.usage_ui.offset = 0;
            this.usage_ui.tab = 0;
            ui::command(json!({"action":"page","value":5}));
            cx.notify();
        }))
}
pub struct UsageUI {
    pub days: u32,
    pub offset: usize,
    pub tab: usize,
    limit: Entity<InputState>,
    period: String,
    timezone: String,
    auto_resume: bool,
    editing: bool,
    error: String,
}
impl UsageUI {
    pub fn open_settings(&mut self) {
        self.editing = false;
        self.error.clear();
    }
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        Self {
            days: 7,
            offset: 0,
            tab: 0,
            limit: cx.new(|cx| InputState::new(window, cx).placeholder("例如 2M、500K")),
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
            let form = Form::horizontal()
                .label_width(px(160.))
                .child(
                    Field::new()
                        .label("Token 额度")
                        .child(div().flex().gap_2().children(
                            [("none", "不限"), ("day", "每天"), ("week", "每周")].map(
                                |(value, label)| {
                                    Button::new(SharedString::from(format!("quota-{value}")))
                                        .ghost()
                                        .label(label)
                                        .when(self.period == value, |b| b.bg(cx.theme().secondary))
                                        .on_click(cx.listener(move |this, _, _, cx| {
                                            this.usage_ui.period = value.into();
                                            cx.notify();
                                        }))
                                },
                            ),
                        )),
                )
                .when(self.period != "none", |f| {
                    f.child(
                        Field::new()
                            .label("额度")
                            .description("输入 + 输出；缓存命中属于输入，支持 K / M")
                            .child(Input::new(&self.limit).aria_label("Token 额度数值")),
                    )
                })
                .when(self.period != "none", |f| {
                    f.child(
                        Field::new().label("额度恢复后").child(
                            div().flex().child(
                                Button::new("quota-auto")
                                    .ghost()
                                    .label(if self.auto_resume {
                                        "自动继续"
                                    } else {
                                        "手动继续"
                                    })
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.usage_ui.auto_resume = !this.usage_ui.auto_resume;
                                        cx.notify();
                                    })),
                            ),
                        ),
                    )
                });
            return ui::page(cx).child(body.child(ui::title("Pot 用量限制"))
                .child(ui::hint("作用于主聊天和所有任务。API 本身不按此周期重置；这是你为 Pot 设置的使用上限。",cx))
                .child(ui::card(cx).child(form).when(self.period!="none",|el|el.child(ui::hint(format!("{} 00:00 重置 · {}",if self.period=="week"{"每周一"}else{"每天"},self.timezone),cx))))
                .when(!self.error.is_empty(),|el|el.child(ui::hint(self.error.clone(),cx))).when(!v.usage_error.is_empty(),|el|el.child(ui::hint(v.usage_error.clone(),cx))).when(!v.usage_notice.is_empty(),|el|el.child(ui::hint(v.usage_notice.clone(),cx)))
                .child(div().flex().gap_2().child(Button::new("save-quota").primary().label("保存").disabled(v.usage_pending||d.is_null()).on_click(cx.listener(|this,_,_,cx|{
                    let u=&mut this.usage_ui;let limit=match parse_count(&u.limit.read(cx).value()){Ok(n)=>n,Err(e)=>{u.error=e.into();cx.notify();return;}};
                    let zone=u.timezone.clone();
                    this.client.usage_action("policy",json!({"period":u.period,"limit":limit,"timezone":zone,"auto_resume":u.auto_resume}));u.error.clear();cx.notify();
                }))).child(Button::new("quota-back").ghost().label("返回").on_click(|_,_,_|ui::command(json!({"action":"page","value":4}))))));
        }
        if detail {
            body=body.child(div().flex().child(Button::new("usage-back").ghost().label("‹ 返回对话").on_click(cx.listener(|this,_,_,cx|{this.show_tasks=false;ui::command(json!({"action":"page","value":if this.client.view.lock().unwrap().selected.is_empty(){0}else{3}}));cx.notify();}))));
        }
        body = body.child(
            div()
                .flex()
                .items_center()
                .justify_between()
                .child(ui::title(if detail {
                    if v.selected.is_empty() {
                        "主聊天用量"
                    } else {
                        "任务用量"
                    }
                } else {
                    "用量"
                }))
                .when(!detail, |el| {
                    el.child(
                        Button::new("quota-settings")
                            .ghost()
                            .label("用量限制")
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
                    .unwrap_or("主聊天".into()),
                cx,
            ));
        }
        if !v.usage_error.is_empty() {
            body = body.child(ui::hint(format!("读取用量失败：{}", v.usage_error), cx));
        }
        if d.is_null() {
            return ui::page(cx).child(body.child(ui::hint("正在读取用量…", cx)));
        }
        let total = if detail { &d["lifetime"] } else { &d["total"] };
        if !detail {
            body = body.child(
                div().flex().gap_2().children(
                    [
                        (7, "最近 7 天"),
                        (30, "最近 30 天"),
                        (90, "最近 90 天"),
                        (365, "最近一年"),
                    ]
                    .map(|(days, label)| {
                        Button::new(("usage-days", days as usize))
                            .ghost()
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
                                    "累计 Tokens"
                                } else {
                                    "总 Tokens"
                                },
                                tokens(total),
                            ),
                            ("输入", n(total, "input")),
                            ("输出", n(total, "output")),
                        ]
                        .map(|(label, value)| {
                            div()
                                .flex_1()
                                .child(ui::hint(label, cx))
                                .child(div().mt_2().text_size(px(26.)).child(count(value)))
                        }),
                    ),
                )
                .child(ui::hint(
                    format!(
                        "输入中缓存命中 {} · {} 次请求{}",
                        count(n(total, "cache")),
                        n(total, "requests"),
                        if n(total, "unknown") > 0 {
                            format!(
                                " · {} 次未返回完整用量，合计可能不完整",
                                n(total, "unknown")
                            )
                        } else {
                            String::new()
                        }
                    ),
                    cx,
                )),
        );
        if detail {
            scroll = scroll.child(
                div().flex().gap_2().children(
                    ["模型请求", "工具", "Skills", "上下文压缩"]
                        .into_iter()
                        .enumerate()
                        .map(|(i, label)| {
                            Button::new(("usage-tab", i))
                                .ghost()
                                .label(label)
                                .when(self.tab == i, |b| b.bg(cx.theme().secondary))
                                .on_click(cx.listener(move |this, _, _, cx| {
                                    this.usage_ui.tab = i;
                                    cx.notify();
                                }))
                        }),
                ),
            );
            match self.tab {
                0 => {
                    let rows = d["requests"].as_array().cloned().unwrap_or_default();
                    if rows.is_empty() {
                        scroll = scroll.child(ui::hint("暂无已记录的模型请求。", cx));
                    }
                    for r in rows {
                        scroll = scroll.child(
                            ui::card(cx)
                                .child(ui::hint(
                                    format!(
                                        "{} · {}",
                                        r["label"].as_str().unwrap_or(""),
                                        r["model"].as_str().unwrap_or("")
                                    ),
                                    cx,
                                ))
                                .child(div().child(format!(
                                    "输入 {} · 输出 {}",
                                    r["input"].as_u64().map(count).unwrap_or("未知".into()),
                                    r["output"].as_u64().map(count).unwrap_or("未知".into())
                                )))
                                .child(ui::hint(
                                    r["turn_label"].as_str().unwrap_or("历史未分类").to_string(),
                                    cx,
                                )),
                        );
                    }
                }
                1 => {
                    scroll=scroll.child(ui::hint("工具内容会进入后续模型请求。以下为返回内容大小估算，不与实际 tokens 相加；估算采用 UTF-8 字节数 ÷ 4。",cx));
                    let rows = d["tools"].as_array().cloned().unwrap_or_default();
                    if rows.is_empty() {
                        scroll = scroll.child(ui::hint("暂无工具调用。", cx));
                    }
                    for r in rows {
                        scroll = scroll.child(
                            ui::card(cx)
                                .child(
                                    div().child(r["name"].as_str().unwrap_or("tool").to_string()),
                                )
                                .child(ui::hint(
                                    format!(
                                        "{} 次调用 · {} 次失败 · {:.1} 秒 · 输出约 {} tokens{}",
                                        n(&r, "calls"),
                                        n(&r, "failed"),
                                        n(&r, "duration_ms") as f64 / 1000.,
                                        count(n(&r, "bytes").div_ceil(4)),
                                        if n(&r, "unfinished") > 0 {
                                            format!(" · {} 次未结束", n(&r, "unfinished"))
                                        } else {
                                            String::new()
                                        }
                                    ),
                                    cx,
                                )),
                        );
                    }
                }
                2 => {
                    scroll = scroll.child(ui::card(cx).child(ui::hint(
                        "当前 Pi 未启用 Skills，暂无可统计的加载用量。",
                        cx,
                    )))
                }
                _ => {
                    scroll = scroll.child(ui::card(cx).child(ui::hint(
                        "当前 Pi 未启用自动上下文压缩，暂无压缩请求用量。",
                        cx,
                    )))
                }
            }
        } else {
            let data = d["buckets"].as_array().cloned().unwrap_or_default();
            let color = cx.theme().primary;
            scroll = scroll.child(
                ui::card(cx)
                    .child(ui::hint(
                        format!(
                            "{} · {} · 今天尚未结束",
                            if self.days == 365 {
                                "每柱最多 7 天"
                            } else {
                                "每柱一天"
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
                                        "所选时间范围暂无用量记录"
                                    } else {
                                        "尚无已确认的 Token 用量"
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
                                                    format!(
                                                        "总量 {} · 输入 {} · 输出 {}",
                                                        count(tokens(d)),
                                                        count(n(d, "input")),
                                                        count(n(d, "output"))
                                                    ),
                                                    cx,
                                                ))
                                                .child(ui::hint(
                                                    format!(
                                                        "其中缓存命中 {} · 未知用量 {} 次",
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
                        .child(div().child("Pot 用量限制"))
                        .child(ui::hint(
                            format!(
                                "{} {} / {} tokens · 预留 {}",
                                if q["policy"]["period"] == "week" {
                                    "本周"
                                } else {
                                    "今日"
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
                            format!("{} 重置", q["reset_label"].as_str().unwrap_or("")),
                            cx,
                        )),
                );
            }
        }
        if d["waiting"].is_object() {
            scroll = scroll.child(
                ui::card(cx)
                    .child(ui::hint(
                        "此会话正在等待 Pot 额度。额度恢复后按设置继续。",
                        cx,
                    ))
                    .child(
                        Button::new("usage-resume")
                            .ghost()
                            .label("继续")
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
                    .label("取消等待")
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
        if n(&d["historical"], "requests") > 0 {
            scroll = scroll.child(ui::hint(
                format!(
                    "另有 {} 次历史请求缺少时间；已保留到会话累计，未计入时间图表。",
                    n(&d["historical"], "requests")
                ),
                cx,
            ));
        }
        if d["waiting"].is_object()
            && d["quota"]["active"] == true
            && n(&d["waiting"], "required") > n(&d["quota"]["policy"], "limit")
        {
            scroll = scroll.child(ui::hint(
                "当前额度小于此会话的上下文预留量；重置也不足以继续，请提高 Pot 额度。",
                cx,
            ));
        }
        body = body.child(scroll);
        if detail && self.tab == 0 {
            body = body.child(
                div()
                    .flex()
                    .items_center()
                    .justify_between()
                    .child(ui::hint(format!("第 {} 页", self.offset / 20 + 1), cx))
                    .child(
                        div()
                            .flex()
                            .gap_2()
                            .child(
                                Button::new("usage-prev")
                                    .ghost()
                                    .label("上一页")
                                    .disabled(self.offset == 0)
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.usage_ui.offset =
                                            this.usage_ui.offset.saturating_sub(20);
                                        cx.notify();
                                    })),
                            )
                            .child(
                                Button::new("usage-next")
                                    .ghost()
                                    .label("下一页")
                                    .disabled(d["next_offset"].is_null())
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.usage_ui.offset += 20;
                                        cx.notify();
                                    })),
                            ),
                    ),
            );
        }
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
        return Err("请输入正整数或 K / M 数值");
    }
    let a = parts[0]
        .parse::<u64>()
        .ok()
        .and_then(|n| n.checked_mul(factor))
        .ok_or("数值过大")?;
    let b = if parts.len() == 2 {
        let scale = 10u64
            .checked_pow(parts[1].len() as u32)
            .ok_or("小数位数过多")?;
        let n = parts[1]
            .parse::<u64>()
            .ok()
            .and_then(|n| n.checked_mul(factor))
            .ok_or("数值过大")?;
        if n % scale != 0 {
            return Err("Token 数必须为整数");
        }
        n / scale
    } else {
        0
    };
    let n = a.checked_add(b).ok_or("数值过大")?;
    if !(1..=9_007_199_254_740_991).contains(&n) {
        return Err("数值超出范围");
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
