//! Shared page structure and Kit settings/Sandbox surfaces.
mod ai;
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
    synced_values: Vec<String>,
    secret_revision: Option<i64>,
    draft_revision: Option<i64>,
    pub advanced: bool,
    commands_open: bool,
    api_advanced: bool,
}
impl Settings {
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        let fields = (0..24)
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
            synced_values: vec![String::new(); 24],
            secret_revision: None,
            draft_revision: None,
            advanced: false,
            commands_open: false,
            api_advanced: false,
        }
    }
    pub fn sync(&mut self, state: &Value, window: &mut Window, cx: &mut Context<Shell>) {
        let revision = state["revision"].as_i64();
        if revision.is_none() || self.revision == revision {
            return;
        }
        let first = self.revision.is_none();
        self.revision = revision;
        let secret_revision = state["secret_revision"].as_i64();
        let clear_secrets = self.secret_revision != secret_revision;
        self.secret_revision = secret_revision;
        if clear_secrets {
            self.api_advanced = false;
            self.commands_open = false;
        }
        let reset_model = self.draft_revision != state["draft_revision"].as_i64();
        self.draft_revision = state["draft_revision"].as_i64();
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
            "draft_model",
            "connection_name",
        ]
        .iter()
        .enumerate()
        {
            let key = if i == 16 { "connection_model" } else { *key };
            let value = state[key].as_str().unwrap_or("").to_owned();
            if first
                || self.synced_values[i] != value
                || (i == 22 && reset_model)
                || (clear_secrets && matches!(i, 0 | 8 | 9 | 14 | 15 | 16 | 23))
            {
                self.synced_values[i] = value.clone();
                self.fields[i].update(cx, |field, cx| field.set_value(value, window, cx));
            }
        }
        if clear_secrets && state["ai_screen"] == "manage" {
            let native = matches!(
                state["connection_kind"].as_str(),
                Some("codex" | "claude" | "agy")
            );
            self.fields[if native { 16 } else { 15 }]
                .read(cx)
                .focus_handle(cx)
                .focus(window, cx);
        }
    }
    pub fn render(&self, state: &Value, cx: &mut Context<Shell>) -> Div {
        let section = state["section"].as_u64().unwrap_or(0);
        let setup = state["setup"].as_bool().unwrap_or(false);
        if setup {
            return self.render_setup(state, cx);
        }
        if section == 1 {
            return self.render_ai(state, cx);
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
            4 => {
                form = self
                    .routing_form(state, cx)
                    .child(input(
                        7,
                        "同时处理的任务数",
                        "默认 5；1–1000。修改全局限制需要重启环境。",
                    ))
                    .child(
                        Field::new().label("周期 Token 额度").child(
                            Button::new("task-usage-policy")
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
                        .child(input(1, "累计请求上限", "0–1000000；0 禁止请求。"))
                        .child(input(2, "输出 token 上限", "支持 K/M，如 128K、1.5M。"))
                        .child(input(3, "每轮请求上限", "1–1000。"));
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
        if setup {
            actions = actions.child(
                Button::new("setup-later")
                    .ghost()
                    .label("稍后设置 · 返回聊天")
                    .on_click(|_, _, _| command(json!({"action":"page","value":0}))),
            );
        }
        if section == 4 {
            actions = actions.child(Button::new("advanced").ghost().label(if self.advanced {"收起高级限制"} else {"高级限制"})
                .on_click(cx.listener(|this,_,_,cx|{this.settings.advanced=!this.settings.advanced;cx.notify();})))
                .child(Button::new("save-task-limits").label("应用全局限制…").on_click(move|_,_,cx|
                    command(json!({"action":"save-agent","key":"","limit":fields[1].read(cx).value(),"tokens":fields[2].read(cx).value(),"per_turn":fields[3].read(cx).value(),"parallel":fields[7].read(cx).value()}))));
        } else if section == 3 {
            actions=actions.child(Button::new("workspace").ghost().label("打开工作目录").on_click(|_,_,_|command(json!({"action":"workspace"}))))
                .child(Button::new("save-sandbox").primary().label("应用并重启…").on_click(move|_,_,cx|command(json!({"action":"save-sandbox","cpus":fields[4].read(cx).value(),"memory":fields[5].read(cx).value(),"disk":fields[6].read(cx).value()}))));
        }
        page(cx).child(
            content()
                .child(title(if setup { "开始使用 Pot" } else { "设置" }))
                .child(
                    div().flex().gap_2().children(
                        [
                            (0, "通用"),
                            (1, "AI 配置"),
                            (4, "任务配置"),
                            (3, "工作环境"),
                        ]
                        .into_iter()
                        .map(|(i, name)| {
                            Button::new(("settings-tab", i as usize))
                                .ghost()
                                .label(name)
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
                        1 => "连接 Agent，选择聊天使用的模型。",
                        4 => "新任务默认沿用聊天配置；已有任务保持原配置。",
                        _ => "资源调整重启后生效，聊天与工作文件保留。实时用量见环境信息。",
                    },
                    cx,
                ))
                .child(
                    div()
                        .id(SharedString::from(format!(
                            "settings-scroll-{}-{}-{}",
                            section,
                            state["ai_screen"].as_str().unwrap_or("main"),
                            state["secret_revision"].as_i64().unwrap_or(0)
                        )))
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
