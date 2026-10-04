//! Shared page structure and Kit settings/Sandbox surfaces.
use crate::{Shell, gpui};
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName,
    button::{Button, ButtonVariants},
    form::{Field, Form},
    input::{Input, InputState},
};
use serde_json::{Value, json};
use std::ffi::CString;

pub const PAGE_WIDTH: f32 = 820.;
pub const SIDEBAR: f32 = 56.;
pub const SANDBOX_HEADER: f32 = 112.;

pub fn command(value: Value) {
    if let Ok(json) = CString::new(value.to_string()) {
        unsafe { crate::pot_bridge_command(json.as_ptr()) };
    }
}
pub fn page(cx: &App) -> Div {
    div()
        .size_full()
        .pl(px(SIDEBAR))
        .bg(cx.theme().background)
        .text_color(cx.theme().foreground)
        .text_size(px(14.))
        .font_family(".AppleSystemUIFont")
}
pub fn content() -> Div {
    div()
        .size_full()
        .max_w(px(PAGE_WIDTH))
        .mx_auto()
        .px_8()
        .py_8()
        .flex()
        .flex_col()
        .gap_5()
}
pub fn title(text: impl Into<SharedString>) -> Div {
    div()
        .text_size(px(24.))
        .font_weight(FontWeight::SEMIBOLD)
        .child(text.into())
}
pub fn hint(text: impl Into<SharedString>, cx: &App) -> Div {
    div()
        .text_size(px(13.))
        .text_color(cx.theme().muted_foreground)
        .child(text.into())
}
pub fn card(cx: &App) -> Div {
    div()
        .w_full()
        .p_5()
        .rounded_lg()
        .border_1()
        .border_color(cx.theme().border)
        .bg(cx.theme().background)
        .flex()
        .flex_col()
        .gap_4()
}

pub struct Settings {
    fields: Vec<Entity<InputState>>,
    revision: Option<i64>,
    pub advanced: bool,
}
impl Settings {
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        let fields = (0..8)
            .map(|i| {
                cx.new(|cx| {
                    InputState::new(window, cx)
                        .masked(i == 0)
                        .placeholder(if i == 0 {
                            "留空保留已保存的密钥"
                        } else {
                            "请输入数值"
                        })
                })
            })
            .collect();
        Self {
            fields,
            revision: None,
            advanced: false,
        }
    }
    pub fn sync(&mut self, state: &Value, window: &mut Window, cx: &mut Context<Shell>) {
        let revision = state["revision"].as_i64();
        if revision.is_none() || self.revision == revision {
            return;
        }
        self.revision = revision;
        for (i, key) in [
            "key", "limit", "tokens", "per_turn", "cpus", "memory", "disk", "parallel",
        ]
        .iter()
        .enumerate()
        {
            let value = state[*key].as_str().unwrap_or("").to_owned();
            self.fields[i].update(cx, |field, cx| field.set_value(value, window, cx));
        }
    }
    pub fn render(&self, state: &Value, cx: &mut Context<Shell>) -> Div {
        let section = state["section"].as_u64().unwrap_or(0);
        let setup = state["setup"].as_bool().unwrap_or(false);
        let mut form = Form::horizontal().label_width(px(160.));
        let label = |name: &'static str, value: &'static str| {
            Field::new().label(name).child(div().py_2().child(value))
        };
        let input = |i: usize, name: &'static str, description: &'static str| {
            Field::new()
                .label(name)
                .description(description)
                .child(Input::new(&self.fields[i]).aria_label(name))
        };
        match section {
            0 => {
                form = form
                    .child(
                        Field::new().label("外观").child(
                            div().flex().gap_2().children(
                                ["跟随系统", "浅色", "深色"]
                                    .iter()
                                    .enumerate()
                                    .map(|(i, name)| {
                                        Button::new(("appearance", i))
                                            .ghost()
                                            .label(*name)
                                            .toggled(state["appearance"].as_u64() == Some(i as u64))
                                            .when(
                                                state["appearance"].as_u64() == Some(i as u64),
                                                |b| b.bg(cx.theme().secondary),
                                            )
                                            .on_click(move |_, _, _| {
                                                command(json!({"action":"appearance","value":i}))
                                            })
                                    }),
                            ),
                        ),
                    )
                    .child(label("快捷键", "⌘D Sandbox · ⌘, 设置 · Shift+Enter 换行"));
            }
            1 => {
                form = form
                    .child(label("执行 Agent", "Pi · 工具调用与任务执行"))
                    .child(label("模型服务", "DeepSeek · DeepSeek Flash"))
                    .child(input(
                        0,
                        "DeepSeek API key",
                        if state["has_key"].as_bool() == Some(true) {
                            "已保存。留空保留；仅用于 Pi 的模型请求。"
                        } else {
                            "为 Pi 连接模型服务，凭据仅保存在本机。"
                        },
                    ));
                form = form.child(input(
                    7,
                    "同时处理的任务数",
                    "默认 5，可设 1–1000；超出的任务排队，主聊天不占用此额度。应用后重启生效。",
                ));
                if self.advanced {
                    form = form
                        .child(input(
                            1,
                            "累计请求上限",
                            "0–1000000，包含已使用的请求；0 表示不允许请求。",
                        ))
                        .child(input(
                            2,
                            "输出 token 上限",
                            "自定义每次输出上限，如 128K、1M、1.5M（K=1000，M=1000000）；实际支持量取决于模型服务。",
                        ))
                        .child(input(
                            3,
                            "每轮请求上限",
                            "1–1000，单轮工具执行可使用的模型请求数。",
                        ));
                }
            }
            _ => {
                form = form
                    .child(label("环境", "本地 Linux VM · Alpine · ARM64"))
                    .child(input(4, "vCPU", "1–32 个核心，受本机可用资源限制"))
                    .child(input(5, "内存", "1–128 GiB，受本机可用资源限制"))
                    .child(input(6, "系统盘", "4–512 GiB，工作文件独立持久保存。"));
            }
        }
        let fields = self.fields.clone();
        let mut actions = div().flex().items_center().justify_end().gap_2();
        if section == 1 {
            actions=actions.child(Button::new("advanced").ghost()
                .label(if self.advanced {"收起用量限制"} else {"用量限制"})
                .on_click(cx.listener(|this,_,_,cx|{this.settings.advanced=!this.settings.advanced;cx.notify();})))
                .child(Button::new("save-agent").primary().label(if setup {"保存并开始"} else {"应用更改…"})
                    .on_click(move|_,_,cx|{
                        command(json!({"action":"save-agent","key":fields[0].read(cx).value(),"limit":fields[1].read(cx).value(),"tokens":fields[2].read(cx).value(),"per_turn":fields[3].read(cx).value(),"parallel":fields[7].read(cx).value()}));
                    }));
        } else if section == 2 {
            actions=actions.child(Button::new("workspace").ghost().label("打开工作目录").on_click(|_,_,_|command(json!({"action":"workspace"}))))
                .child(Button::new("save-sandbox").primary().label("应用并重启…").on_click(move|_,_,cx|command(json!({"action":"save-sandbox","cpus":fields[4].read(cx).value(),"memory":fields[5].read(cx).value(),"disk":fields[6].read(cx).value()}))));
        }
        page(cx).child(
            content()
                .child(title(if setup { "开始使用 Pot" } else { "设置" }))
                .child(
                    div().flex().gap_2().children(
                        ["通用", "Agent", "Sandbox"]
                            .iter()
                            .enumerate()
                            .map(|(i, name)| {
                                Button::new(("settings-tab", i))
                                    .ghost()
                                    .label(*name)
                                    .toggled(section == i as u64)
                                    .when(section == i as u64, |b| b.bg(cx.theme().secondary))
                                    .on_click(move |_, _, _| {
                                        command(json!({"action":"section","value":i}))
                                    })
                            }),
                    ),
                )
                .child(hint(
                    match section {
                        0 => "让 Pot 按你的习惯工作。",
                        1 => "Pi 执行任务，DeepSeek 提供模型能力。更改连接会重启 Sandbox。",
                        _ => "资源调整重启后生效，聊天与工作文件保留。实时用量见环境信息。",
                    },
                    cx,
                ))
                .child(
                    div()
                        .id("settings-scroll")
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .child(card(cx).child(form).child(actions))
                        .child(
                            div()
                                .mt_3()
                                .child(hint(state["result"].as_str().unwrap_or("").to_owned(), cx)),
                        ),
                ),
        )
    }
}

pub fn sandbox(state: &Value, cx: &mut Context<Shell>) -> Div {
    let connected = state["phase"].as_str() == Some("running")
        && state["viewer"]["connected"].as_bool() == Some(true);
    let human = state["viewer"]["human"].as_bool() == Some(true);
    let running = state["phase"].as_str() == Some("running");
    let action = |id: &'static str, icon: IconName, tooltip: &'static str| {
        Button::new(id)
            .ghost()
            .icon(icon)
            .tooltip(tooltip)
            .accessibility_label(tooltip)
            .disabled(!running)
            .on_click(move |_, _, _| command(json!({"action":"viewer","id":id})))
    };
    page(cx)
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_8()
                .py_6()
                .flex()
                .flex_col()
                .gap_2()
                .child(
                    div()
                        .flex()
                        .items_center()
                        .justify_between()
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap_3()
                                .child(title("Sandbox"))
                                .child(
                                    Button::new("connection-state")
                                        .ghost()
                                        .label("●")
                                        .text_color(rgb(if connected {
                                            0x26a269
                                        } else {
                                            0xd99a27
                                        }))
                                        .tooltip(if connected { "已连接" } else { "未连接" })
                                        .accessibility_label(if connected {
                                            "已连接"
                                        } else {
                                            "未连接"
                                        }),
                                ),
                        )
                        .child(
                            div()
                                .flex()
                                .items_center()
                                .gap_2()
                                .child(
                                    Button::new("take")
                                        .primary()
                                        .label(if human { "交还控制" } else { "接管" })
                                        .disabled(!connected)
                                        .on_click(|_, _, _| {
                                            command(json!({"action":"viewer","id":"take"}))
                                        }),
                                )
                                .child(action("info", IconName::Info, "环境信息"))
                                .child(action("connect", IconName::RefreshCw, "重新连接"))
                                .child(
                                    Button::new("sandbox-settings")
                                        .ghost()
                                        .icon(IconName::Settings)
                                        .tooltip("环境设置")
                                        .accessibility_label("环境设置")
                                        .on_click(|_, _, _| command(json!({"action":"manage"}))),
                                )
                                .child(action("more", IconName::Ellipsis, "更多操作")),
                        ),
                )
                .child(hint("查看工作进度，或接管桌面继续操作。", cx)),
        )
        .when(!running, |el| {
            el.child(
                content().h_auto().child(
                    card(cx)
                        .child(hint("Sandbox 尚未就绪，启动后将在这里显示桌面。", cx))
                        .child(
                            Button::new("start-sandbox")
                                .primary()
                                .label("启动 Sandbox")
                                .disabled(!matches!(
                                    state["phase"].as_str(),
                                    Some("stopped" | "failed")
                                ))
                                .on_click(|_, _, _| command(json!({"action":"start"}))),
                        ),
                ),
            )
        })
}
