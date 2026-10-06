//! Shared page structure and Kit settings/Sandbox surfaces.
mod ai;
pub(crate) use ai::brand_icon;
mod routing;
use crate::{Shell, design, gpui};
use gpui::{prelude::*, *};
use gpui_kit::component::menu::DropdownMenu;
use gpui_kit::component::{
    ActiveTheme, Disableable, Icon, IconName, Sizable,
    button::{Button, ButtonVariants},
    form::{Field, Form},
    input::{Input, InputState},
};
use serde_json::{Value, json};
use std::ffi::CString;

pub const PAGE_WIDTH: f32 = 820.;
pub const SIDEBAR: f32 = 56.;
pub const SANDBOX_HEADER: f32 = 56.;

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
        .gap_4()
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
        .rounded(px(10.))
        .border_1()
        .border_color(cx.theme().border)
        .bg(design::surface(cx))
        .flex()
        .flex_col()
        .gap_4()
}

/// Quiet grouped choices, with compact native proportions.
pub fn segments(cx: &App) -> Div {
    div()
        .flex()
        .self_start()
        .flex_wrap()
        .items_center()
        .gap_1()
        .p_1()
        .rounded(px(7.))
        .bg(design::navigation(cx))
        .border_1()
        .border_color(cx.theme().border)
}
pub fn form(cx: &App) -> Form {
    (if design::compact(cx) {
        Form::vertical()
    } else {
        Form::horizontal().label_width(px(144.))
    })
    .small()
    .gap_4()
}
pub fn field(label: impl Into<SharedString>) -> Field {
    let label = label.into();
    Field::new().label_fn(move |_, cx| {
        div()
            .w_full()
            .text_size(px(14.))
            .font_weight(FontWeight::MEDIUM)
            .when(!design::compact(cx), |el| el.text_right())
            .child(label.clone())
    })
}
/// Animate a newly opened panel without replaying motion on routine refreshes.
pub fn sheet(panel: Div, key: SharedString, cx: &App) -> AnyElement {
    if cx.reduce_motion() {
        panel.into_any_element()
    } else {
        panel
            .with_animation(
                key,
                Animation::new(std::time::Duration::from_millis(200)),
                |el, t| el.opacity(0.92 + 0.08 * t),
            )
            .into_any_element()
    }
}
pub fn section(text: impl Into<SharedString>) -> Div {
    div()
        .text_size(px(16.))
        .font_weight(FontWeight::SEMIBOLD)
        .child(text.into())
}
pub fn empty(icon: IconName, text: impl Into<SharedString>, cx: &App) -> Div {
    div()
        .w_full()
        .py_12()
        .px_6()
        .flex()
        .flex_col()
        .items_center()
        .gap_3()
        .text_color(cx.theme().muted_foreground)
        .text_size(px(13.))
        .child(Icon::new(icon).size(px(24.)))
        .child(text.into())
}
pub fn loading(text: impl Into<SharedString>, cx: &App) -> Div {
    div()
        .w_full()
        .py_8()
        .flex()
        .items_center()
        .justify_center()
        .gap_3()
        .text_size(px(13.))
        .text_color(cx.theme().muted_foreground)
        .child(gpui_kit::component::spinner::Spinner::new().small())
        .child(text.into())
}
pub fn notice(text: impl Into<SharedString>, error: bool, cx: &App) -> Div {
    let color = if error {
        cx.theme().danger
    } else {
        design::attention(cx)
    };
    div()
        .w_full()
        .p_3()
        .rounded(px(8.))
        .bg(color.opacity(0.07))
        .flex()
        .gap_2()
        .text_size(px(13.))
        .text_color(color)
        .child(Icon::new(IconName::Info).size(px(15.)))
        .child(text.into())
}

pub fn retry(cx: &mut Context<Shell>) -> Button {
    Button::new("retry-refresh")
        .ghost()
        .small()
        .icon(IconName::RefreshCw)
        .label(crate::i18n::text("ui.retry"))
        .on_click(cx.listener(|this, _, _, cx| {
            this.client.refresh();
            cx.notify();
        }))
}

pub struct Settings {
    pub scroll: ScrollHandle,
    locale: String,
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
    fn mobile_access(&self, view: &crate::client::View, cx: &mut Context<Shell>) -> Div {
        let mut panel = div()
            .flex()
            .flex_col()
            .gap_2()
            .p_3()
            .border_1()
            .border_color(cx.theme().border)
            .rounded_lg()
            .child(crate::i18n::text("mobile.access"));
        if view.access.is_null() {
            return panel.child(crate::i18n::text("mobile.configure"));
        }
        let state = &view.access;
        let origin = state["origin"].as_str().unwrap_or_default();
        panel = panel
            .child(origin.to_owned())
            .child(crate::i18n::text("mobile.owner-scope"));
        if let Ok(code) = qrcode::QrCode::new(origin) {
            let width = code.width();
            let colors = code.to_colors();
            let rows = (0..width + 8).map(|y| {
                div().flex().children((0..width + 8).map(|x| {
                    let dark = x >= 4
                        && y >= 4
                        && x < width + 4
                        && y < width + 4
                        && colors[(y - 4) * width + x - 4] == qrcode::Color::Dark;
                    div().w(px(4.)).h(px(4.)).bg(if dark {
                        gpui::rgb(0x000000)
                    } else {
                        gpui::rgb(0xffffff)
                    })
                }))
            });
            panel = panel.child(div().flex().flex_col().items_start().children(rows));
        }
        let mut controls = div().flex().flex_wrap().gap_2();
        for (action, key) in [
            (
                if state["enabled"] == true {
                    "disable"
                } else {
                    "enable"
                },
                if state["enabled"] == true {
                    "mobile.disable"
                } else {
                    "mobile.enable"
                },
            ),
            ("pair", "mobile.pair"),
            ("close_enrollment", "mobile.close-enrollment"),
        ] {
            controls = controls.child(
                Button::new(action)
                    .ghost()
                    .label(crate::i18n::text(key))
                    .disabled(view.access_pending)
                    .on_click(cx.listener(move |this, _, _, _| {
                        this.client.access_command(json!({"action":action}))
                    })),
            );
        }
        panel = panel.child(controls).child(view.access_notice.clone());
        for (kind, action) in [
            ("pending", "approve"),
            ("credentials", "revoke_credential"),
            ("sessions", "revoke_session"),
        ] {
            if let Some(items) = state[kind].as_array() {
                for item in items {
                    let id = item["id"].as_str().unwrap_or_default().to_owned();
                    let label = if kind == "pending" {
                        format!(
                            "{} · {}",
                            item["name"].as_str().unwrap_or_default(),
                            item["code"].as_str().unwrap_or_default()
                        )
                    } else {
                        item["name"].as_str().unwrap_or(&id).to_owned()
                    };
                    panel = panel.child(
                        div().flex().gap_2().items_center().child(label).child(
                            Button::new(id.clone())
                                .ghost()
                                .label(crate::i18n::text(if action == "approve" {
                                    "mobile.approve"
                                } else {
                                    "mobile.revoke"
                                }))
                                .disabled(view.access_pending)
                                .on_click(cx.listener(move |this, _, _, _| {
                                    this.client.access_command(json!({"action":action,"id":id}))
                                })),
                        ),
                    );
                }
            }
        }
        panel
    }

    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        let fields = (0..24)
            .map(|i| {
                cx.new(|cx| {
                    InputState::new(window, cx)
                        .masked(i == 0 || i == 15)
                        .placeholder(if i == 0 {
                            crate::i18n::text("ui.leave-blank-to-keep-the-saved-key")
                        } else {
                            crate::i18n::text("ui.enter-a-value")
                        })
                })
            })
            .collect();
        Self {
            scroll: ScrollHandle::new(),
            locale: String::new(),
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
    pub fn localize(&mut self, window: &mut Window, cx: &mut Context<Shell>) -> bool {
        let locale = rust_i18n::locale().to_string();
        if self.locale == locale {
            return false;
        }
        self.locale = locale;
        for (i, field) in self.fields.iter().enumerate() {
            field.update(cx, |input, cx| {
                input.set_placeholder(
                    crate::i18n::text(if i == 0 {
                        "ui.leave-blank-to-keep-the-saved-key"
                    } else {
                        "ui.enter-a-value"
                    }),
                    window,
                    cx,
                )
            });
        }
        true
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
        let field_keys: [&str; 24] = [
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
            "",
            "",
            "",
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
        ];
        for (i, key) in field_keys.iter().enumerate() {
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
            let native = matches!(state["connection_kind"].as_str(), Some("codex"));
            self.fields[if native { 16 } else { 15 }]
                .read(cx)
                .focus_handle(cx)
                .focus(window, cx);
        }
    }
    pub fn render(
        &self,
        state: &Value,
        view: &crate::client::View,
        cx: &mut Context<Shell>,
    ) -> Div {
        let section = state["section"].as_u64().unwrap_or(0);
        if section == 1 {
            return self.render_ai(state, cx);
        }
        let mut form = form(cx);
        let label =
            |name: &'static str, value: &'static str| field(name).child(div().py_2().child(value));
        let input = |i: usize, name: &'static str, description: &'static str| {
            field(name)
                .description(description)
                .child(Input::new(&self.fields[i]).aria_label(name))
        };
        match section {
            0 => {
                form = form.child(
                    field(crate::i18n::text("mobile.access")).child(self.mobile_access(view, cx)),
                );
                form = form
                    .child(field(crate::i18n::text("settings.language"))
                        .description(crate::i18n::text("settings.language.hint"))
                        .child(Button::new("language-picker").ghost().small()
                            .label(if state["locale"] == "zh-CN" { "简体中文" } else { "English" })
                            .dropdown_menu({
                                let locale = state["locale"].as_str().unwrap_or("en").to_owned();
                                move |menu, _, _| {
                                    [ ("en", "English"), ("zh-CN", "简体中文") ].into_iter().fold(menu, |menu, (value, name)| {
                                        menu.item(gpui_kit::component::menu::PopupMenuItem::new(name)
                                            .checked(locale == value)
                                            .on_click(move |_, _, _| command(json!({"action":"language","value":value}))))
                                    })
                                }
                            })))
                    .child(field(crate::i18n::text("ui.appearance")).child(
                        segments(cx).children([crate::i18n::text("ui.system"), crate::i18n::text("ui.light"), crate::i18n::text("ui.dark")].iter().enumerate().map(
                            |(i, name)| {
                                Button::new(("appearance", i))
                                    .ghost()
                                    .small()
                                    .label(*name)
                                    .toggled(state["appearance"].as_u64() == Some(i as u64))
                                    .when(state["appearance"].as_u64() == Some(i as u64), |b| {
                                        b.bg(cx.theme().secondary)
                                    })
                                    .on_click(move |_, _, _| {
                                        command(json!({"action":"appearance","value":i}))
                                    })
                            },
                        )),
                    ))
                    .child(label(
                        crate::i18n::text("ui.keyboard-shortcuts"),
                        crate::i18n::text("ui.back-forward-d-sandbox-settings-shift-enter"),
                    ));
            }
            4 => {
                form = self
                    .routing_form(state, cx)
                    .child(input(
                        7,
                        crate::i18n::text("ui.concurrent-tasks"),
                        crate::i18n::text("ui.default-5-range-1-1000-apply-global"),
                    ))
                    .child(
                        field(crate::i18n::text("ui.periodic-token-allowance")).child(
                            Button::new("task-usage-policy")
                                .ghost()
                                .small()
                                .label(crate::i18n::text("ui.set-allowance-and-automatic-resume"))
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
                            crate::i18n::text("ui.lifetime-request-limit"),
                            crate::i18n::text("ui.0-1000000-0-disables-requests"),
                        ))
                        .child(input(
                            2,
                            crate::i18n::text("ui.output-token-limit"),
                            crate::i18n::text("ui.supports-k-m-such-as-128k-or"),
                        ))
                        .child(input(
                            3,
                            crate::i18n::text("ui.requests-per-turn"),
                            "1–1000。",
                        ));
                }
            }
            _ => {
                form = form.child(
                    field(crate::i18n::text("ui.environment-management")).child(
                        div()
                            .flex()
                            .flex_col()
                            .gap_3()
                            .child(hint(
                                crate::i18n::text("ui.select-an-environment-on-the-sandbox-page"),
                                cx,
                            ))
                            .child(
                                Button::new("open-sandbox-page")
                                    .primary()
                                    .label(crate::i18n::text("ui.open-sandbox"))
                                    .on_click(|_, _, _| {
                                        command(json!({"action":"page","value":1}))
                                    }),
                            ),
                    ),
                );
            }
        }
        let fields = self.fields.clone();
        let mut actions = div().flex().items_center().justify_end().gap_2();
        if section == 4 {
            actions = actions.child(Button::new("advanced").ghost().small().label(if self.advanced {crate::i18n::text("ui.hide-advanced-limits")} else {crate::i18n::text("ui.advanced-limits")})
                .on_click(cx.listener(|this,_,_,cx|{this.settings.advanced = !this.settings.advanced;cx.notify();})))
                .child(Button::new("save-task-limits").primary().small().label(crate::i18n::text("ui.apply-global-limits")).on_click(move|_,_,cx|
                    command(json!({"action":"save-agent","key":"","limit":fields[1].read(cx).value(),"tokens":fields[2].read(cx).value(),"per_turn":fields[3].read(cx).value(),"parallel":fields[7].read(cx).value()}))));
        }

        page(cx).child(
            content()
                .child(title(
                    [
                        crate::i18n::text("ui.general"),
                        crate::i18n::text("ui.ai-configuration"),
                        crate::i18n::text("ui.ai-configuration"),
                        crate::i18n::text("ui.work-environment"),
                        crate::i18n::text("ui.task-configuration"),
                    ]
                    .get(section as usize)
                    .copied()
                    .unwrap_or(crate::i18n::text("ui.settings")),
                ))
                .child(
                    segments(cx).children(
                        [
                            (0, crate::i18n::text("ui.general")),
                            (1, crate::i18n::text("ui.ai-configuration")),
                            (4, crate::i18n::text("ui.task-configuration")),
                            (3, crate::i18n::text("ui.work-environment")),
                        ]
                        .into_iter()
                        .map(|(i, name)| {
                            Button::new(("settings-tab", i as usize))
                                .ghost()
                                .small()
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
                        0 => crate::i18n::text("ui.make-pot-work-the-way-you-prefer"),
                        1 => crate::i18n::text("ui.connect-an-agent-and-choose-the-model"),
                        4 => crate::i18n::text("ui.new-tasks-follow-the-chat-configuration-by"),
                        _ => {
                            crate::i18n::text("ui.resource-changes-apply-after-restarting-chat-and")
                        }
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
                        .track_scroll(&self.scroll)
                        .flex_1()
                        .min_h_0()
                        .overflow_y_scroll()
                        .child(card(cx).child(form))
                        .child(div().mt_3().when(
                            !state["result"].as_str().unwrap_or("").is_empty(),
                            |el| {
                                el.child(notice(
                                    state["result"].as_str().unwrap_or("").to_owned(),
                                    false,
                                    cx,
                                ))
                            },
                        )),
                )
                .child(
                    actions
                        .flex_wrap()
                        .w_full()
                        .pt_3()
                        .border_t_1()
                        .border_color(cx.theme().border)
                        .flex_shrink_0(),
                ),
        )
    }
}

pub const SANDBOX_LIST: f32 = 224.;

pub fn sandbox(state: &Value, cx: &mut Context<Shell>) -> Div {
    let selected = state["sandbox_view"].as_str().unwrap_or("default");
    let default = state["sandbox_default"].as_str().unwrap_or("default");
    let items = state["sandboxes"].as_array().cloned().unwrap_or_default();
    let current = items.iter().find(|item| item["id"] == selected);
    let name = current
        .and_then(|item| item["name"].as_str())
        .unwrap_or("Sandbox");
    let phase = state["phase"].as_str().unwrap_or("unknown");
    let connected = phase == "running" && state["viewer"]["connected"] == true;
    let human = state["viewer"]["human"] == true;
    let pending = state["sandbox_busy"] == true;
    let mut list = div()
        .id("sandbox-list")
        .flex_1()
        .min_h_0()
        .overflow_y_scroll()
        .p_3()
        .flex()
        .flex_col()
        .gap_2();
    for item in items.iter() {
        let id = item["id"].as_str().unwrap_or("").to_owned();
        let target = id.clone();
        let active = id == selected;
        let row = div()
            .w_full()
            .min_w_0()
            .flex()
            .flex_col()
            .gap_2()
            .child(
                div()
                    .font_weight(FontWeight::MEDIUM)
                    .overflow_hidden()
                    .child(item["name"].as_str().unwrap_or("Linux").to_owned()),
            )
            .child(hint(
                crate::i18n::tr!(
                    "ui.running-tasks",
                    crate::i18n::phase(item["state"]["phase"].as_str().unwrap_or("unknown")),
                    item["running_tasks"].as_u64().unwrap_or(0)
                ),
                cx,
            ))
            .when(id == default, |row| {
                row.child(hint(crate::i18n::text("ui.execution-default"), cx))
            });
        let row = Button::new(SharedString::from(format!("sandbox-{id}")))
            .ghost()
            .w_full()
            .h_auto()
            .p_3()
            .bg(if active {
                cx.theme().secondary
            } else {
                design::navigation(cx)
            })
            .accessibility_label(crate::i18n::tr!(
                "ui.select-sandbox",
                item["name"].as_str().unwrap_or("Linux")
            ))
            .child(row)
            .on_click(move |_, _, _| command(json!({"action":"sandbox-view","id":target})));
        list = list.child(row);
    }
    let sidebar =
        div()
            .w(px(SANDBOX_LIST))
            .h_full()
            .flex_shrink_0()
            .bg(design::navigation(cx))
            .border_r_1()
            .border_color(cx.theme().border)
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(SANDBOX_HEADER))
                    .px_4()
                    .flex()
                    .items_center()
                    .justify_between()
                    .child("Sandbox")
                    .child(
                        Button::new("sandbox-new")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("ui.new"))
                            .disabled(pending)
                            .on_click(|_, _, _| command(json!({"action":"sandbox-new"}))),
                    ),
            )
            .child(list)
            .when(
                state["sandbox_error"]
                    .as_str()
                    .is_some_and(|e| !e.is_empty()),
                |el| {
                    el.child(div().p_3().text_color(design::attention(cx)).child(
                        crate::i18n::message(state["sandbox_error"].as_str().unwrap_or("")),
                    ))
                },
            );
    let desktop = div()
        .flex_1()
        .min_w_0()
        .h_full()
        .flex()
        .flex_col()
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_6()
                .flex()
                .items_center()
                .gap_3()
                .border_b_1()
                .border_color(cx.theme().border)
                .child(
                    div()
                        .flex_1()
                        .overflow_hidden()
                        .font_weight(FontWeight::SEMIBOLD)
                        .child(name.to_owned()),
                )
                .child(hint(crate::i18n::phase(phase), cx))
                .when(selected == default, |el| {
                    el.child(hint(crate::i18n::text("ui.execution-default"), cx))
                }),
        )
        .child(
            div()
                .h(px(SANDBOX_HEADER))
                .px_6()
                .flex()
                .items_center()
                .justify_between()
                .border_b_1()
                .border_color(cx.theme().border)
                .child(hint(
                    if pending {
                        crate::i18n::text("ui.working")
                    } else if !connected {
                        crate::i18n::text("ui.desktop-disconnected")
                    } else if human {
                        crate::i18n::text("ui.you-control-the-desktop")
                    } else {
                        crate::i18n::text("ui.agent-controls-the-desktop")
                    },
                    cx,
                ))
                .child(
                    div()
                        .flex()
                        .items_center()
                        .gap_2()
                        .child(
                            Button::new("take")
                                .primary()
                                .small()
                                .label(if human {
                                    crate::i18n::text("ui.give-back-control")
                                } else {
                                    crate::i18n::text("ui.take-over")
                                })
                                .disabled(!connected || pending)
                                .on_click(|_, _, _| {
                                    command(json!({"action":"viewer","id":"take"}))
                                }),
                        )
                        .child(
                            Button::new("sandbox-settings")
                                .ghost()
                                .small()
                                .icon(IconName::Settings)
                                .tooltip(crate::i18n::text("ui.environment-settings"))
                                .accessibility_label(crate::i18n::text("ui.environment-settings"))
                                .disabled(pending)
                                .on_click(|_, _, _| command(json!({"action":"sandbox-settings"}))),
                        )
                        .child(
                            Button::new("sandbox-more")
                                .ghost()
                                .small()
                                .icon(IconName::Ellipsis)
                                .tooltip(crate::i18n::text("ui.more-actions"))
                                .accessibility_label(crate::i18n::text("ui.more-actions"))
                                .disabled(pending)
                                .on_click(|_, _, _| {
                                    command(json!({"action":"viewer","id":"more"}))
                                }),
                        ),
                ),
        )
        .when(!connected, |el| {
            el.child(
                div()
                    .flex_1()
                    .p_8()
                    .flex()
                    .flex_col()
                    .items_center()
                    .justify_center()
                    .gap_4()
                    .child(hint(
                        if phase == "running" {
                            crate::i18n::text("ui.connecting-to-desktop")
                        } else {
                            crate::i18n::text("ui.this-environment-is-stopped-start-the-desktop")
                        },
                        cx,
                    ))
                    .child(
                        Button::new("start-sandbox")
                            .primary()
                            .label(crate::i18n::text("ui.start-desktop"))
                            .disabled(pending || !matches!(phase, "running" | "stopped" | "failed"))
                            .on_click(|_, _, _| command(json!({"action":"start"}))),
                    ),
            )
        });
    page(cx).flex().child(sidebar).child(desktop)
}
