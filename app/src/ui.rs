//! Shared page structure and Kit settings/Sandbox surfaces.
mod routing;
mod setup;
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

/// Shape against the actual column width: GPUI's intrinsic text measurement can
/// otherwise clip a child before the inherited ellipsis is painted.
pub fn single_line_title(
    text: &str,
    size: f32,
    width: Pixels,
    window: &Window,
    cx: &App,
) -> SharedString {
    let style = TextStyle {
        font_family: ".AppleSystemUIFont".into(),
        font_weight: FontWeight::MEDIUM,
        color: cx.theme().foreground,
        ..Default::default()
    };
    let runs = [style.to_run(text.len())];
    let text: SharedString = text.to_owned().into();
    if window
        .text_system()
        .shape_line(text.clone(), px(size), &runs, None)
        .width
        <= width
    {
        return text;
    }
    use unicode_segmentation::UnicodeSegmentation;
    let boundaries: Vec<usize> = text
        .grapheme_indices(true)
        .map(|(index, _)| index)
        .collect();
    let mut lo = 0;
    let mut hi = boundaries.len();
    while lo < hi {
        let mid = (lo + hi).div_ceil(2);
        let end = boundaries.get(mid).copied().unwrap_or(text.len());
        let candidate: SharedString = format!("{}…", &text[..end]).into();
        let run = style.to_run(candidate.len());
        if window
            .text_system()
            .shape_line(candidate, px(size), &[run], None)
            .width
            <= width
        {
            lo = mid;
        } else {
            hi = mid - 1;
        }
    }
    format!(
        "{}…",
        &text[..boundaries.get(lo).copied().unwrap_or(text.len())]
    )
    .into()
}

pub fn title_tooltip(title: String, window: &mut Window, cx: &mut App) -> AnyView {
    gpui_kit::component::tooltip::Tooltip::element(move |_, _| {
        div().w(px(440.)).whitespace_normal().child(title.clone())
    })
    .max_w(px(480.))
    .build(window, cx)
}

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
    commands_open: bool,
}
impl Settings {
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        let fields = (0..22)
            .map(|i| {
                cx.new(|cx| {
                    InputState::new(window, cx)
                        .masked(i == 0 || i == 15)
                        .placeholder(if i == 0 {
                            "留空保留已保存的密钥"
                        } else {
                            "请输入"
                        })
                })
            })
            .collect();
        Self {
            fields,
            revision: None,
            advanced: false,
            commands_open: false,
        }
    }
    pub fn sync(&mut self, state: &Value, window: &mut Window, cx: &mut Context<Shell>) {
        let revision = state["revision"].as_i64();
        if revision.is_none() || self.revision == revision {
            return;
        }
        self.revision = revision;
        for (i, key) in [
            "key",
            "limit",
            "tokens",
            "per_turn",
            "cpus",
            "memory",
            "disk",
            "parallel",
            "executable",
            "argv",
            "codex_model",
            "agy_model",
            "",
            "claude_model",
            "api_endpoint",
            "api_key",
            "api_model",
            "routing_name",
            "routing_model",
            "routing_profile_limit",
            "routing_provider_limit",
            "provider_tokens",
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
        if setup {
            return self.render_setup(state, cx);
        }
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
        let native_available = |engine: &str| {
            state["agents"].as_array().is_some_and(|agents| {
                agents.iter().any(|a| {
                    a["id"] == engine
                        && a["installation"] == "present"
                        && a["status_check"] != "failed"
                        && (a["authentication"] == "authenticated"
                            || a["authentication"] == "unknown")
                })
            })
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
                let fields = self.fields.clone();
                form = form.child(input(10, "Codex 模型", "填写当前账户可用的模型标识；状态只通过官方查询检测。"))
                    .child(Field::new().label("主 Agent").child(Button::new("select-codex").primary().label("使用 Codex").disabled(!native_available("codex"))
                        .on_click(move |_, _, cx| command(json!({"action":"select-codex","model":fields[10].read(cx).value()})))));
                let fields = self.fields.clone();
                form = form.child(input(13, "Claude 模型", "填写当前账户可用的模型标识；状态只通过官方查询检测。"))
                    .child(Field::new().label("主 Agent").child(Button::new("select-claude").label("使用 Claude").disabled(!native_available("claude"))
                        .on_click(move |_, _, cx| command(json!({"action":"select-claude","model":fields[13].read(cx).value()})))));
                let fields = self.fields.clone();
                form = form.child(input(11, "AGY 模型", "支持原生主聊和续聊；后台任务与只读汇总尚未开放。登录请在 Terminal 处理。"))
                    .child(Field::new().label("主 Agent").child(Button::new("select-agy").label("使用 AGY").disabled(!native_available("agy"))
                        .on_click(move |_, _, cx| command(json!({"action":"select-agy","model":fields[11].read(cx).value()})))));
                form = form.child(label(
                    "模型 API",
                    "Pi 可直接连接模型服务；无需 CLI 登录或付费测试。",
                ));
                let formats = [
                    ("openai_chat", "OpenAI 兼容"),
                    ("openai_responses", "Responses"),
                    ("anthropic_messages", "Anthropic"),
                    ("gemini", "Gemini"),
                ];
                form = form.child(Field::new().label("API 格式").child(div().flex().flex_wrap().gap_2().children(formats.into_iter().enumerate().map(|(i,(protocol,name))| {
                    Button::new(("api-format",i)).ghost().label(name)
                        .toggled(state["api_protocol"] == protocol)
                        .on_click(move |_,_,_| command(json!({"action":"api-protocol","protocol":protocol})))
                }))));
                form = form
                    .child(input(
                        14,
                        "API endpoint",
                        "完整 HTTPS 请求地址，如 https://api.example.com/v1/chat/completions",
                    ))
                    .child(input(
                        15,
                        "API key",
                        if state["has_api_key"] == true {
                            "已保存；相同 endpoint 留空可保留。"
                        } else {
                            "仅保存到 Sandbox 私有配置。"
                        },
                    ))
                    .child(input(16, "API 模型", "填写服务商提供的模型标识。"));
                let fields = self.fields.clone();
                form = form.child(Field::new().label("主 Agent").child(Button::new("save-api").label("使用此 API")
                    .on_click(move |_,_,cx| command(json!({"action":"save-api","endpoint":fields[14].read(cx).value(),"key":fields[15].read(cx).value(),"model":fields[16].read(cx).value()})))));
                form = form
                    .child(label("API 连接", "Pi · 工具调用与任务执行"))
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
                form = form.child(
                    Field::new().label("周期 Token 额度").child(
                        Button::new("agent-usage-policy")
                            .ghost()
                            .label("设置额度与自动继续 →")
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.usage_ui.open_settings();
                                command(json!({"action":"page","value":6}));
                                cx.notify();
                            })),
                    ),
                );
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
            2 => {
                form = form.child(
                    Field::new().label("登录状态").child(
                        Button::new("refresh-agents")
                            .ghost()
                            .label("刷新状态")
                            .disabled(state["discovering"].as_bool() == Some(true))
                            .on_click(|_, _, _| command(json!({"action":"agent-refresh"}))),
                    ),
                );
                for agent in state["agents"].as_array().into_iter().flatten() {
                    let id = agent["id"].as_str().unwrap_or("").to_owned();
                    let status = if state["discovering"] == true {
                        "检测中"
                    } else if agent["installation"] == "missing" {
                        "未安装"
                    } else if agent["status_check"] == "failed" {
                        "检查失败"
                    } else {
                        match agent["authentication"].as_str() {
                            Some("authenticated") => "已登录",
                            Some("required") => "未登录",
                            _ => "状态未知",
                        }
                    };
                    let name = id.clone();
                    form = form.child(
                        Field::new()
                            .label(name)
                            .description(if id == "pi" {
                                "运行环境已随 Pot 提供；通过 API 设置连接模型。"
                            } else if id == "codex" {
                                "原生 App Server · 官方登录状态只读检测。"
                            } else if id == "claude" {
                                "原生 CLI · 官方登录状态只读检测。"
                            } else {
                                "原生 CLI 主聊 · 无官方登录查询，状态保持未知；任务隔离待验收。"
                            })
                            .child(div().py_2().child(status)),
                    );
                }
                form = form.child(
                    Field::new().label("当前 Agent").child(
                        Button::new("pi-command-details")
                            .ghost()
                            .label("调用命令")
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.settings.commands_open = !this.settings.commands_open;
                                cx.notify();
                            })),
                    ),
                );
                if self.commands_open {
                    form = form
                        .child(input(
                            8,
                            "调用程序",
                            "可填可执行文件或 wrapper 路径；直接执行，不经过 shell。",
                        ))
                        .child(input(
                            9,
                            "额外参数（JSON）",
                            "字符串数组。Runtime 追加 RPC、会话和工具策略参数；冲突参数会被拒绝。",
                        ));
                }
            }
            4 => {
                form = self.routing_form(state, cx);
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
        if setup {
            actions = actions.child(
                Button::new("setup-later")
                    .ghost()
                    .label("稍后设置 · 打开 Terminal")
                    .on_click(|_, _, _| command(json!({"action":"terminal","value":true}))),
            );
        }
        if section == 1 {
            actions=actions.child(Button::new("advanced").ghost()
                .label(if self.advanced {"收起用量限制"} else {"用量限制"})
                .on_click(cx.listener(|this,_,_,cx|{this.settings.advanced=!this.settings.advanced;cx.notify();})))
                .child(Button::new("save-agent").primary().label(if setup {"保存并开始"} else {"应用更改…"})
                    .on_click(move|_,_,cx|{
                        command(json!({"action":"save-agent","key":fields[0].read(cx).value(),"limit":fields[1].read(cx).value(),"tokens":fields[2].read(cx).value(),"per_turn":fields[3].read(cx).value(),"parallel":fields[7].read(cx).value()}));
                    }));
        } else if section == 2 {
            actions = actions.child(
                Button::new("open-terminal")
                    .ghost()
                    .label("打开 Terminal")
                    .on_click(|_, _, _| command(json!({"action":"terminal"}))),
            );
            actions = actions.child(
                Button::new("reopen-setup")
                    .ghost()
                    .label("安装其他 Agent")
                    .on_click(|_, _, _| command(json!({"action":"setup-stage","value":0}))),
            );
            actions = actions.child(Button::new("save-invocation").primary().label("保存调用命令").on_click(move|_,_,cx|command(json!({"action":"save-invocation","executable":fields[8].read(cx).value(),"argv":fields[9].read(cx).value()}))));
        } else if section == 3 {
            actions=actions.child(Button::new("workspace").ghost().label("打开工作目录").on_click(|_,_,_|command(json!({"action":"workspace"}))))
                .child(Button::new("save-sandbox").primary().label("应用并重启…").on_click(move|_,_,cx|command(json!({"action":"save-sandbox","cpus":fields[4].read(cx).value(),"memory":fields[5].read(cx).value(),"disk":fields[6].read(cx).value()}))));
        }
        page(cx).child(
            content()
                .child(title(if setup { "开始使用 Pot" } else { "设置" }))
                .child(
                    div().flex().gap_2().children(
                        ["通用", "助手", "Agent", "Sandbox", "任务路由"]
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
                        1 => "明确选择主 Agent 或配置模型 API，并设置任务与用量额度。",
                        2 => "查看 Agent 状态或编辑调用命令。保存不会调用模型。",
                        4 => "管理已保存的配置、后台任务默认和并发限制。汇总沿用主会话。",
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
                ),
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

pub fn terminal(state: &Value, cx: &mut Context<Shell>) -> Div {
    let running = state["phase"] == "running";
    page(cx)
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_8()
                .py_6()
                .flex()
                .items_center()
                .justify_between()
                .child(title("Terminal"))
                .child(
                    div()
                        .flex()
                        .gap_2()
                        .child(
                            Button::new("terminal-reconnect")
                                .ghost()
                                .label("重新连接")
                                .disabled(!running)
                                .on_click(|_, _, _| command(json!({"action":"terminal"}))),
                        )
                        .child(
                            Button::new("terminal-close")
                                .ghost()
                                .label("关闭终端")
                                .disabled(!running)
                                .on_click(|_, _, _| command(json!({"action":"terminal-close"}))),
                        ),
                ),
        )
        .when(!running, |el| {
            el.child(
                content().h_auto().child(
                    card(cx)
                        .child(hint("Sandbox Runtime 尚未就绪，启动后可使用终端。", cx))
                        .child(
                            Button::new("terminal-start")
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
