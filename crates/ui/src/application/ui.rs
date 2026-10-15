//! Shared page structure and Kit settings/Sandbox surfaces.
mod ai;
pub(crate) use pot_ui::pages::brand_icon;
mod routing;
use crate::application::{Shell, gpui};
use crate::pages::{Control, FieldHelp};
use gpui::{prelude::*, *};
use gpui_kit::component::menu::DropdownMenu;
use gpui_kit::component::{
    ActiveTheme, Disableable, IconName, Sizable,
    button::{Button, ButtonVariants},
    form::Form,
    input::{Input, InputEvent, InputState},
};
use serde_json::{Value, json};

pub const TITLEBAR: f32 = 44.;

/// Shape against the actual column width: GPUI's intrinsic text measurement can
/// otherwise clip a child before the inherited ellipsis is painted.
pub use pot_ui::pages::title_tooltip;

pub fn command(value: Value) {
    let value = if value["action"] == "page" {
        json!({"action":"navigation-command","value":value})
    } else if super::settings_state::SettingsState::handles(value["action"].as_str().unwrap_or(""))
    {
        json!({"action":"configuration-command","value":value})
    } else {
        value
    };
    super::platform::with(|p| p.command(value));
}
pub use pot_ui::pages::{card, empty, field, form, hint, notice, section, segments, title};
pub fn page(cx: &App) -> Div {
    pot_ui::pages::page(
        pot_ui::pages::Layout {
            compact: pot_ui::design::compact(cx),
        },
        cx,
    )
}
pub fn content(cx: &App) -> Div {
    pot_ui::pages::content(pot_ui::pages::Layout {
        compact: pot_ui::design::compact(cx),
    })
}

/// Keep navigation identical across all settings screens.
fn settings_header(selected: u64, busy: bool, location: Option<Div>, cx: &App) -> Div {
    div()
        .flex()
        .items_center()
        .justify_between()
        .flex_wrap()
        .gap_3()
        .flex_shrink_0()
        .child(
            segments(cx).children(
                [
                    (0, crate::application::i18n::text("ui.general")),
                    (
                        1,
                        crate::application::i18n::text(if pot_ui::design::mobile(cx) {
                            "settings.ai-tab"
                        } else {
                            "ui.ai-configuration"
                        }),
                    ),
                    (
                        4,
                        crate::application::i18n::text(if pot_ui::design::mobile(cx) {
                            "settings.tasks-tab"
                        } else {
                            "ui.task-configuration"
                        }),
                    ),
                ]
                .into_iter()
                .map(|(i, name)| {
                    Button::new(("settings-tab", i as usize))
                        .ghost()
                        .control(cx)
                        .when(!pot_ui::design::touch(cx), |b| b.h(px(28.)))
                        .label(name)
                        .toggled(selected == i)
                        .when(selected == i, |b| {
                            b.bg(pot_ui::design::surface(cx)).shadow_sm()
                        })
                        .on_click(move |_, _, _| command(json!({"action":"section","value":i})))
                }),
            ),
        )
        .child(
            div()
                .flex()
                .items_center()
                .gap_2()
                .when_some(location, |el, location| el.child(location))
                .child(div().size(px(16.)).when(busy, |el| {
                    el.child(gpui_kit::component::spinner::Spinner::new().small())
                })),
        )
}

pub struct Settings {
    pub scroll: ScrollHandle,
    reveal: pot_ui::pages::InputReveal,
    locale: String,
    fields: Vec<Entity<InputState>>,
    revision: Option<i64>,
    synced_values: Vec<String>,
    secret_revision: Option<i64>,
    draft_revision: Option<i64>,
    pub advanced: bool,
    api_advanced: bool,
    shortcuts_open: bool,
}
impl Settings {
    pub(super) fn adopt_revision(&mut self, state: &Value) {
        self.revision = state["revision"].as_i64();
        self.secret_revision = state["secret_revision"].as_i64();
        self.draft_revision = state["draft_revision"].as_i64();
    }
    pub(super) fn editor_values(&self, state: &Value, cx: &App) -> Value {
        let fields: &[usize] = match state["ai_screen"].as_str() {
            Some("manage") => &[14, 15, 16, 23, 24, 25],
            Some("main-edit") => &[22],
            _ => return Value::Null,
        };
        json!({"fields":fields.iter().map(|&i|self.fields[i].read(cx).value().to_string()).collect::<Vec<_>>(),
            "protocol":state["api_protocol"],"profile":state["draft_profile"]})
    }
    pub(super) fn reveal_connection_error(
        &mut self,
        value: &Value,
        window: &mut Window,
        cx: &mut Context<Shell>,
    ) {
        let invalid = if value["model"].as_str().unwrap_or("").trim().is_empty() {
            Some(16)
        } else if value["name"].as_str().unwrap_or("").trim().is_empty() {
            Some(23)
        } else {
            [("context", 24), ("output", 25)]
                .into_iter()
                .find_map(|(key, index)| {
                    let text = value[key].as_str().unwrap_or("").trim();
                    (!text.is_empty()
                        && text
                            .parse::<u64>()
                            .ok()
                            .is_none_or(|n| n == 0 || n > 9_007_199_254_740_991))
                    .then_some(index)
                })
        };
        if let Some(index) = invalid {
            self.reveal.reset();
            if index >= 23 {
                self.api_advanced = true;
            }
            self.fields[index]
                .read(cx)
                .focus_handle(cx)
                .focus(window, cx);
        }
    }
    pub fn new(window: &mut Window, cx: &mut Context<Shell>) -> Self {
        let fields: Vec<Entity<InputState>> = (0..27)
            .map(|i| {
                cx.new(|cx| {
                    InputState::new(window, cx)
                        .masked(i == 0 || i == 15)
                        .placeholder(if i == 0 {
                            crate::application::i18n::text("ui.leave-blank-to-keep-the-saved-key")
                        } else {
                            crate::application::i18n::text("ui.enter-a-value")
                        })
                })
            })
            .collect();
        for i in [16, 24, 26] {
            cx.subscribe(&fields[i], move |_, field, event: &InputEvent, cx| {
                if matches!(event, InputEvent::Change) {
                    let value = field.read(cx).value().to_string();
                    if i == 16 {
                        command(json!({"action":"ai-model-input","model":value}));
                    }
                    if i == 24 {
                        command(json!({"action":"ai-context-input","value":value}));
                    }
                    cx.notify();
                }
            })
            .detach();
        }
        Self {
            scroll: ScrollHandle::new(),
            reveal: Default::default(),
            locale: String::new(),
            fields,
            revision: None,
            synced_values: vec![String::new(); 27],
            secret_revision: None,
            draft_revision: None,
            advanced: false,
            api_advanced: false,
            shortcuts_open: false,
        }
    }
    pub fn localize(&mut self, window: &mut Window, cx: &mut Context<Shell>) -> bool {
        let locale = super::i18n::locale().to_string();
        if self.locale == locale {
            return false;
        }
        self.locale = locale;
        self.reveal.reset();
        for (i, field) in self.fields.iter().enumerate() {
            if matches!(i, 24 | 25) {
                continue;
            }
            field.update(cx, |input, cx| {
                input.set_placeholder(
                    crate::application::i18n::text(if i == 0 {
                        "ui.leave-blank-to-keep-the-saved-key"
                    } else if i == 26 {
                        "ai.search-providers"
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
        }
        let reset_model = self.draft_revision != state["draft_revision"].as_i64();
        self.draft_revision = state["draft_revision"].as_i64();
        let field_keys: [&str; 27] = [
            "key",
            "limit",
            "tokens",
            "per_turn",
            "cpus",
            "memory",
            "disk",
            "parallel",
            "",
            "",
            "",
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
            "connection_context",
            "connection_output",
            "",
        ];
        for (i, key) in field_keys.iter().enumerate() {
            if i == 26 {
                continue;
            }
            let key = if i == 16 { "connection_model" } else { *key };
            let value = state[key].as_str().unwrap_or("").to_owned();
            if first
                || self.synced_values[i] != value
                || (i == 22 && reset_model)
                || (clear_secrets && matches!(i, 0 | 8 | 9 | 14 | 15 | 16 | 23 | 24 | 25))
            {
                self.synced_values[i] = value.clone();
                self.fields[i].update(cx, |field, cx| field.set_value(value, window, cx));
            }
        }
        for (i, key, default) in [(24, "context_default", 32768), (25, "output_default", 8192)] {
            let placeholder = state[key].as_u64().unwrap_or(default).to_string();
            self.fields[i].update(cx, |field, cx| {
                field.set_placeholder(placeholder, window, cx)
            });
        }
        self.fields[26].update(cx, |field, cx| {
            field.set_placeholder(
                crate::application::i18n::text("ai.search-providers"),
                window,
                cx,
            )
        });
        if clear_secrets && state["ai_screen"] == "manage" && !pot_ui::design::narrow_editor(cx) {
            self.fields[15].read(cx).focus_handle(cx).focus(window, cx);
        }
    }
    pub fn render(&self, state: &Value, location: Option<Div>, cx: &mut Context<Shell>) -> Div {
        let section = state["section"].as_u64().unwrap_or(0);
        if section == 1 {
            return self.render_ai(state, location, cx);
        }
        let mut form = form(cx);
        let mut preferences = div().flex().flex_col().gap_4();
        let input = |i: usize, name: &'static str, description: &'static str| {
            field(name).help(description).child(
                div()
                    .w(px(128.))
                    .child(Input::new(&self.fields[i]).control(cx).aria_label(name)),
            )
        };
        match section {
            0 => {
                preferences = preferences.child(
                    div().flex().items_center().justify_between().gap_4().flex_wrap()
                        .child(div().flex().flex_col().gap_1()
                            .child(crate::application::i18n::text("settings.language")))
                        .child(Button::new("language-picker").control(cx).w(px(180.)).when(pot_ui::design::mobile(cx), |b| b.w_full()).dropdown_caret(true)
                            .label(pot_ui::localization::LANGUAGES.iter().find(|(locale, _)| state["locale"] == *locale).map(|(_, name)| *name).unwrap_or("English"))
                            .dropdown_menu({
                                let locale = state["locale"].as_str().unwrap_or("en").to_owned();
                                move |menu, _, cx| pot_ui::localization::LANGUAGES.iter().copied().fold(menu, |menu, (value, name)| {
                                    menu.item(pot_ui::pages::menu_item(name, cx).checked(locale == value)
                                        .on_click(move |_, _, _| command(json!({"action":"language","value":value}))))
                                })
                            }))
                ).child(
                    div().flex().items_center().justify_between().gap_4().flex_wrap()
                        .pt_4().border_t_1().border_color(cx.theme().border)
                        .child(crate::application::i18n::text("ui.appearance"))
                        .child(segments(cx).when(pot_ui::design::mobile(cx), |el| el.w_full()).children([
                            crate::application::i18n::text("ui.system"), crate::application::i18n::text("ui.light"), crate::application::i18n::text("ui.dark")
                        ].into_iter().enumerate().map(|(i, name)| {
                            Button::new(("appearance", i)).ghost().control(cx).min_w(px(64.)).when(!pot_ui::design::touch(cx), |b| b.h(px(28.)))
                                .label(name).toggled(state["appearance"].as_u64() == Some(i as u64))
                                .when(state["appearance"].as_u64() == Some(i as u64), |b| b.bg(pot_ui::design::surface(cx)).shadow_sm())
                                .on_click(move |_, _, _| command(json!({"action":"appearance","value":i})))
                        })))
                );
            }
            4 => {
                form = form
                    .child(input(
                        7,
                        crate::application::i18n::text("ui.concurrent-tasks"),
                        crate::application::i18n::text("ui.concurrent-tasks-hint"),
                    ))
                    .child(
                        field(crate::application::i18n::text(
                            "ui.periodic-token-allowance",
                        ))
                        .child(
                            Button::new("task-usage-policy")
                                .ghost()
                                .control(cx)
                                .label(crate::application::i18n::text(
                                    "ui.set-allowance-and-automatic-resume",
                                ))
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
                            crate::application::i18n::text("ui.lifetime-request-limit"),
                            crate::application::i18n::text("ui.0-1000000-0-disables-requests"),
                        ))
                        .child(input(
                            2,
                            crate::application::i18n::text("ui.output-token-limit"),
                            crate::application::i18n::text("ui.supports-k-m-such-as-128k-or"),
                        ))
                        .child(input(
                            3,
                            crate::application::i18n::text("ui.requests-per-turn"),
                            "1–1000。",
                        ));
                }
            }
            _ => {}
        }
        let fields = self.fields.clone();
        let mut actions = div().flex().items_center().justify_end().gap_2();
        if section == 4 {
            actions = actions.child(Button::new("advanced").ghost().control(cx).label(if self.advanced {crate::application::i18n::text("ui.hide-advanced-limits")} else {crate::application::i18n::text("ui.advanced-limits")})
                .on_click(cx.listener(|this,_,_,cx|{this.settings.advanced = !this.settings.advanced;cx.notify();})))
                .child(Button::new("save-task-limits").primary().control(cx).label(crate::application::i18n::text("ui.apply-global-limits")).on_click(move|_,_,cx|
                    command(json!({"action":"save-agent","limit":fields[1].read(cx).value(),"tokens":fields[2].read(cx).value(),"per_turn":fields[3].read(cx).value(),"parallel":fields[7].read(cx).value()}))));
        }

        page(cx).child(
            content(cx)
                .child(settings_header(
                    section,
                    state["routing_busy"] == true,
                    location,
                    cx,
                ))
                .when(section != 0, |el| {
                    el.child(hint(
                        match section {
                            0 => crate::application::i18n::text(
                                "ui.make-pot-work-the-way-you-prefer",
                            ),
                            1 => crate::application::i18n::text(
                                "ui.connect-an-agent-and-choose-the-model",
                            ),
                            4 => crate::application::i18n::text(
                                "ui.new-tasks-follow-the-chat-configuration-by",
                            ),
                            _ => crate::application::i18n::text(
                                "ui.resource-changes-apply-after-restarting-chat-and",
                            ),
                        },
                        cx,
                    ))
                })
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
                        .when(section == 4, |el| {
                            el.child(
                                card(cx)
                                    .mb_4()
                                    .child(pot_ui::pages::section(crate::application::i18n::text(
                                        "ui.task-configuration",
                                    )))
                                    .child(self.routing_form(state, cx)),
                            )
                        })
                        .when(
                            section == 0
                                || super::platform::with(|p| {
                                    p.capabilities().local_execution_settings
                                }),
                            |el| {
                                el.child(
                                    card(cx)
                                        .child(pot_ui::pages::section(
                                            crate::application::i18n::text(if section == 0 {
                                                "ui.general"
                                            } else {
                                                "ui.execution-limits"
                                            }),
                                        ))
                                        .when(section == 0, |el| el.child(preferences))
                                        .when(
                                            section != 0
                                                && super::platform::with(|p| {
                                                    p.capabilities().local_execution_settings
                                                }),
                                            |el| el.child(form),
                                        ),
                                )
                            },
                        )
                        .when(section == 0, |el| {
                            el.child(
                                card(cx)
                                    .mt_4()
                                    .child(
                                        Button::new("keyboard-shortcuts-toggle")
                                            .ghost()
                                            .control(cx)
                                            .label(crate::application::i18n::text(
                                                "ui.keyboard-shortcuts",
                                            ))
                                            .when(pot_ui::design::mobile(cx), |b| {
                                                b.icon(if self.shortcuts_open {
                                                    IconName::ChevronDown
                                                } else {
                                                    IconName::ChevronRight
                                                })
                                            })
                                            .on_click(cx.listener(|this, _, _, cx| {
                                                this.settings.shortcuts_open =
                                                    !this.settings.shortcuts_open;
                                                cx.notify();
                                            })),
                                    )
                                    .when(
                                        !pot_ui::design::mobile(cx) || self.shortcuts_open,
                                        |el| {
                                            el.children(
                                                [
                                                    ("settings.shortcuts.parent", "⌘ ["),
                                                    ("machines.title", "⌘ D"),
                                                    ("ui.settings", "⌘ ,"),
                                                    ("settings.shortcuts.new-line", "⇧ ↵"),
                                                ]
                                                .into_iter()
                                                .enumerate()
                                                .map(|(index, (label, keys))| {
                                                    div()
                                                        .flex()
                                                        .items_center()
                                                        .justify_between()
                                                        .gap_4()
                                                        .when(index > 0, |row| {
                                                            row.pt_3()
                                                                .border_t_1()
                                                                .border_color(cx.theme().border)
                                                        })
                                                        .child(hint(
                                                            crate::application::i18n::text(label),
                                                            cx,
                                                        ))
                                                        .child(
                                                            div()
                                                                .min_w(px(64.))
                                                                .px_2()
                                                                .py_1()
                                                                .rounded(px(5.))
                                                                .border_1()
                                                                .border_color(cx.theme().border)
                                                                .bg(cx.theme().muted)
                                                                .text_center()
                                                                .text_size(px(12.))
                                                                .child(shortcut_keys(keys)),
                                                        )
                                                }),
                                            )
                                        },
                                    ),
                            )
                        })
                        .when(
                            section == 0
                                && super::platform::with(|p| p.capabilities().resource_cache),
                            |el| {
                                el.child(
                                    card(cx).mt_4().child(
                                        Button::new("settings-resource-cache")
                                            .ghost()
                                            .control(cx)
                                            .self_start()
                                            .label(crate::application::i18n::text(
                                                "resources.cache",
                                            ))
                                            .on_click(|_, _, _| {
                                                command(json!({"action":"resource-cache"}))
                                            }),
                                    ),
                                )
                            },
                        )
                        .when(
                            section == 0
                                && super::platform::with(|p| p.capabilities().browser_access),
                            |el| {
                                el.child(
                                    card(cx).mt_4().children(
                                        [
                                            ("install", "mobile.install"),
                                            (
                                                "notifications",
                                                "mobile.enable-message-notifications",
                                            ),
                                            (
                                                "notifications-off",
                                                "mobile.disable-message-notifications",
                                            ),
                                            ("configure-token", "web.change-token"),
                                            ("logout", "mobile.sign-out"),
                                        ]
                                        .into_iter()
                                        .map(
                                            |(action, key)| {
                                                Button::new(action)
                                                    .ghost()
                                                    .control(cx)
                                                    .self_start()
                                                    .label(crate::application::i18n::text(key))
                                                    .on_click(move |_, _, _| {
                                                        command(json!({"action":action}))
                                                    })
                                            },
                                        ),
                                    ),
                                )
                            },
                        )
                        .child(div().mt_3().when(
                            !state["result"].as_str().unwrap_or("").is_empty(),
                            |el| {
                                el.child(notice(
                                    crate::application::i18n::message(
                                        state["result"].as_str().unwrap_or(""),
                                    ),
                                    false,
                                    cx,
                                ))
                            },
                        )),
                )
                .when(
                    section == 4
                        && super::platform::with(|p| p.capabilities().local_execution_settings),
                    |el| {
                        el.child(
                            actions
                                .flex_wrap()
                                .w_full()
                                .pt_3()
                                .border_t_1()
                                .border_color(cx.theme().border)
                                .flex_shrink_0(),
                        )
                    },
                ),
        )
    }
}

fn shortcut_keys(keys: &str) -> Div {
    use gpui_kit::assets::IconName;
    let mut row = div().flex().items_center().justify_center().gap_1();
    if keys.starts_with('⌘') {
        row = if super::platform::with(|p| p.capabilities().command_key) {
            row.child(gpui_kit::component::Icon::new(IconName::Command).size(px(12.)))
        } else {
            row.child("Ctrl")
        };
        row.child(keys.trim_start_matches('⌘').trim().to_owned())
    } else {
        row.child(gpui_kit::component::Icon::new(IconName::ArrowBigUp).size(px(12.)))
            .child(gpui_kit::component::Icon::new(IconName::CornerDownLeft).size(px(12.)))
    }
}
