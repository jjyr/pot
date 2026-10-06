use gpui_kit::component::{
    ActiveTheme, Disableable,
    button::{Button, ButtonVariants},
    input::{Input, InputState, Textarea, TextareaState},
};
use gpui_kit::{prelude::*, *};
use serde_json::{Value, json};
use std::{borrow::Cow, cell::RefCell, collections::HashSet, rc::Rc, sync::Arc};
use wasm_bindgen::prelude::*;
gpui_kit::assets::icon_assets!(MobileAssets, [Copy, Check, ExternalLink]);
thread_local! {
    static SEND: RefCell<pot_ui::state::SendState> = RefCell::new(pot_ui::state::SendState::default());
    static VIEW: RefCell<Option<Entity<Mobile>>> = const { RefCell::new(None) };
    static HANDLE: RefCell<Option<ApplicationHandle>> = const { RefCell::new(None) };
}
#[wasm_bindgen]
extern "C" {
    #[wasm_bindgen(js_namespace = window, js_name = potAction)]
    fn action(name: &str, value: &str);
}
struct Mobile {
    data: Value,
    page: String,
    composer: Entity<TextareaState>,
    drafts: pot_ui::state::Drafts<Entity<TextareaState>>,
    expanded: HashSet<String>,
    conversation: String,
    accepted: String,
    search: Entity<InputState>,
}
impl Mobile {
    fn label(&self, key: &str) -> String {
        pot_ui::localization::Catalog::new(self.data["locale"].as_str().unwrap_or("en"))
            .text(key)
            .into()
    }
}
impl Render for Mobile {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let mode = if self.data["dark"] == true {
            gpui_kit::component::ThemeMode::Dark
        } else {
            gpui_kit::component::ThemeMode::Light
        };
        if gpui_kit::component::Theme::global(cx).mode != mode {
            gpui_kit::component::Theme::change(mode, Some(window), cx);
            crate::design::install(cx);
            gpui_kit::component::Theme::update(cx, |theme| {
                theme.font_family = "IBM Plex Sans".into();
                theme.font_size = px(16.);
            });
        }
        let conversation = self.data["conversation"]
            .as_str()
            .unwrap_or("main")
            .to_owned();
        if conversation != self.conversation {
            self.drafts.save(&self.conversation, self.composer.clone());
            self.composer = self
                .drafts
                .get(&conversation)
                .unwrap_or_else(|| cx.new(|cx| TextareaState::new(window, cx)));
            self.conversation = conversation;
        }
        if let Ok(receipt) =
            serde_json::from_value::<pot_ui::state::Receipt>(self.data["accepted"].clone())
            && receipt.id != self.accepted
        {
            let composer = if receipt.conversation == self.conversation {
                Some(self.composer.clone())
            } else {
                self.drafts.get(&receipt.conversation)
            };
            if let Some(composer) = composer
                && receipt.clears(&receipt.conversation, &composer.read(cx).value())
            {
                composer.update(cx, |input, cx| input.set_value("", window, cx));
            }
            self.accepted = receipt.id;
        }
        cx.set_reduce_motion(self.data["reduce_motion"] == true);
        cx.set_global(crate::design::Viewport(window.viewport_size().width));
        let snapshot =
            serde_json::from_value::<pot_protocol::Snapshot>(self.data["snapshot"].clone()).ok();
        let pending = SEND.with(|state| state.borrow().pending().is_some());
        let can_send = pot_ui::state::can_send(
            snapshot.as_ref(),
            self.conversation != "main",
            pending,
            self.data["connected"] == true,
        );
        let mut content = div()
            .id("mobile-content")
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .p_4()
            .flex()
            .flex_col()
            .gap_3();
        if self.page == "chat" || self.page == "task" {
            content = content.gap_5();
            if let Some(snapshot) = &snapshot {
                let labels = pot_ui::chat::Labels::new(|key| self.label(key));
                let handler: pot_ui::chat::ChatHandler = Rc::new(cx.listener(
                    |this, event: &pot_ui::chat::ChatAction, _, cx| {
                        match event {
                            pot_ui::chat::ChatAction::ToggleTool(id) => {
                                if !this.expanded.remove(id) {
                                    this.expanded.insert(id.clone());
                                }
                            }
                            pot_ui::chat::ChatAction::OpenTask(id) => action("task", id),
                        }
                        cx.notify();
                    },
                ));
                for (index, m) in snapshot.messages.iter().enumerate() {
                    let catalog = pot_ui::localization::Catalog::new(
                        self.data["locale"].as_str().unwrap_or("en"),
                    );
                    let source = pot_ui::chat::display_source(
                        m,
                        |text| catalog.report(text),
                        |text| catalog.message(text),
                    );
                    content = content.child(pot_ui::chat::message(
                        pot_ui::chat::MessageView {
                            message: m,
                            source: &source,
                            labels: &labels,
                            artifact: None,
                            options: pot_ui::chat::MessageOptions {
                                dark: self.data["dark"] == true,
                                expanded: self.expanded.contains(&m.id),
                                latest_reply: snapshot.turn.is_none()
                                    && index + 1 == snapshot.messages.len()
                                    && m.role == pot_protocol::Role::Assistant,
                                touch: window.viewport_size().width < px(600.),
                                available_width: f32::from(window.viewport_size().width) - 32.,
                            },
                        },
                        handler.clone(),
                        cx,
                    ));
                }
            }
        } else if self.page == "tasks" {
            let mut filters = div().flex().flex_wrap().gap_2();
            for (scope, key) in [
                ("active", "mobile.active"),
                ("attention", "mobile.attention"),
                ("archived", "mobile.archived"),
                ("all", "mobile.all"),
            ] {
                filters = filters.child(
                    Button::new(scope)
                        .h(px(44.))
                        .ghost()
                        .label(self.label(key))
                        .on_click(move |_, _, _| action("filter", scope)),
                );
            }
            content = content
                .child(filters)
                .child(Input::new(&self.search).aria_label(self.label("mobile.search")))
                .child(
                    Button::new("search")
                        .h(px(44.))
                        .label(self.label("mobile.search"))
                        .on_click(cx.listener(|this, _, _, cx| {
                            action("search", &this.search.read(cx).value())
                        })),
                );

            if let Some(tasks) = self.data["tasks"].as_array() {
                for t in tasks {
                    let id = t["id"].as_str().unwrap_or_default().to_owned();
                    let title = t["title"].as_str().unwrap_or_default().to_owned();
                    content = content.child(
                        Button::new(id.clone())
                            .h(px(44.))
                            .w_full()
                            .ghost()
                            .label(title)
                            .on_click(move |_, _, _| action("task", &id)),
                    );
                }
            }
            content = content.child(
                div()
                    .flex()
                    .gap_2()
                    .child(
                        Button::new("previous")
                            .h(px(44.))
                            .ghost()
                            .label(self.label("mobile.previous"))
                            .on_click(|_, _, _| action("previous", "")),
                    )
                    .child(
                        Button::new("next")
                            .h(px(44.))
                            .ghost()
                            .label(self.label("mobile.next"))
                            .on_click(|_, _, _| action("next", "")),
                    ),
            );
        } else if self.page == "sandbox" {
            content = content.child(
                self.label("mobile.viewing-an-environment-does-not-change-the-execution-default"),
            );
            if let Some(items) = self.data["sandboxes"].as_array() {
                for item in items {
                    content = content.child(
                        div()
                            .p_3()
                            .border_1()
                            .border_color(cx.theme().border)
                            .rounded_lg()
                            .child(format!(
                                "{} · {}",
                                item["name"].as_str().unwrap_or_default(),
                                item["state"]["phase"].as_str().unwrap_or_default()
                            )),
                    );
                }
            }
            content =
                content.child(self.label("mobile.desktop-access-awaits-permission-qualification"));
        } else if self.page == "usage" {
            let totals = &self.data["usage"]["total"];
            for (field, key) in [
                ("requests", "mobile.requests"),
                ("input", "mobile.input-tokens"),
                ("output", "mobile.output-tokens"),
                ("unknown", "mobile.unknown-usage"),
            ] {
                content = content.child(div().p_3().child(format!(
                        "{}: {}",
                        self.label(key),
                        totals[field]
                            .as_u64()
                            .map(|v| v.to_string())
                            .unwrap_or_else(|| "—".into())
                    )));
            }
        } else if self.page == "schedules" {
            if let Some(items) = self.data["schedules"]["items"].as_array() {
                for item in items {
                    content =
                        content.child(
                            div()
                                .p_3()
                                .border_1()
                                .border_color(cx.theme().border)
                                .rounded_lg()
                                .child(
                                    item["spec"]["title"]
                                        .as_str()
                                        .unwrap_or_default()
                                        .to_owned(),
                                )
                                .child(div().child(
                                    item["next_label"].as_str().unwrap_or_default().to_owned(),
                                )),
                        );
                }
            }
        } else {
            content = content
                .child(
                    Button::new("install")
                        .h(px(44.))
                        .label(self.label(if self.data["installed"] == true {
                            "mobile.installed"
                        } else {
                            "mobile.install"
                        }))
                        .on_click(|_, _, _| action("install", "")),
                )
                .child(
                    Button::new("notifications")
                        .h(px(44.))
                        .label(self.label("mobile.enable-message-notifications"))
                        .on_click(|_, _, _| action("notifications", "")),
                )
                .child(
                    Button::new("privacy")
                        .h(px(44.))
                        .label(self.label("mobile.use-private-notification-previews"))
                        .on_click(|_, _, _| action("notifications", "private")),
                )
                .child(
                    Button::new("notifications-off")
                        .h(px(44.))
                        .label(self.label("mobile.disable-message-notifications"))
                        .on_click(|_, _, _| action("notifications-off", "")),
                )
                .child(
                    Button::new("usage")
                        .h(px(44.))
                        .label(self.label("mobile.usage"))
                        .on_click(|_, _, _| action("page", "usage")),
                )
                .child(
                    Button::new("schedules")
                        .h(px(44.))
                        .label(self.label("mobile.schedules"))
                        .on_click(|_, _, _| action("page", "schedules")),
                )
                .child(self.label("mobile.owner-access-to-this-pot-instance-and-its-environments"))
                .child(
                    Button::new("language")
                        .h(px(44.))
                        .label(self.label("mobile.language"))
                        .on_click(|_, _, _| action("language", "")),
                )
                .child(
                    Button::new("logout")
                        .h(px(44.))
                        .label(self.label("mobile.sign-out"))
                        .on_click(|_, _, _| action("logout", "")),
                );
        }
        let mut main = div()
            .size_full()
            .bg(cx.theme().background)
            .text_color(cx.theme().foreground)
            .flex()
            .flex_col()
            .child(div().h(px(48.)).p_3().child("Pot"))
            .child(
                div().text_sm().px_4().child(
                    pot_ui::localization::Catalog::new(
                        self.data["locale"].as_str().unwrap_or("en"),
                    )
                    .message(self.data["notice"].as_str().unwrap_or_default()),
                ),
            )
            .child(content);
        if self.page == "chat" || self.page == "task" {
            let send = self.label("mobile.send");
            let stop = self.label("mobile.stop");
            main = main.child(
                div()
                    .p_3()
                    .flex()
                    .flex_col()
                    .gap_2()
                    .child(
                        Textarea::new(&self.composer)
                            .aria_label(self.label("mobile.message"))
                            .h(px(80.)),
                    )
                    .child(
                        div()
                            .flex()
                            .gap_2()
                            .child(
                                Button::new("send")
                                    .h(px(44.))
                                    .primary()
                                    .label(send)
                                    .disabled(!can_send)
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        let text = this.composer.read(cx).value().to_string();
                                        if !text.trim().is_empty() {
                                            action("send", &text);
                                        }
                                    })),
                            )
                            .child(
                                Button::new("retry")
                                    .h(px(44.))
                                    .ghost()
                                    .label(self.label("mobile.retry"))
                                    .on_click(|_, _, _| action("retry", "")),
                            )
                            .child(
                                Button::new("stop")
                                    .h(px(44.))
                                    .ghost()
                                    .label(stop)
                                    .on_click(|_, _, _| action("stop", "")),
                            ),
                    ),
            );
        }
        let mut nav = div()
            .h(px(56.))
            .flex()
            .justify_between()
            .px_2()
            .border_t_1()
            .border_color(cx.theme().border);
        for (name, key) in [
            ("chat", "mobile.chat"),
            ("tasks", "mobile.tasks"),
            ("sandbox", "mobile.sandbox"),
            ("more", "mobile.more"),
        ] {
            nav = nav.child(
                Button::new(name)
                    .h(px(44.))
                    .ghost()
                    .label(self.label(key))
                    .on_click(move |_, _, _| action("page", name)),
            );
        }
        main.child(nav)
    }
}
#[wasm_bindgen]
pub fn update_view(data: &str) {
    let Ok(data) = serde_json::from_str::<Value>(data) else {
        return;
    };
    HANDLE.with(|cell| {
        if let Some(app) = cell.borrow().as_ref() {
            VIEW.with(|view| {
                if let Some(view) = view.borrow().as_ref() {
                    app.update(|cx| {
                        view.update(cx, |this, cx| {
                            this.page = data["page"].as_str().unwrap_or("chat").into();
                            this.data = data;
                            cx.notify();
                        })
                    });
                }
            });
        }
    });
}
#[wasm_bindgen]
pub fn run() {
    console_error_panic_hook::set_once();
    gpui_kit::platform::web_init();
    let platform = Rc::new(
        gpui_kit::web::WebPlatform::new_with_backend_and_font_fallback(
            false,
            gpui_kit::web::WebBackendPreference::Auto,
            gpui_kit::web::CanvasFontFallback::EmojiAndCjk,
        ),
    );
    let client = Arc::new(platform.fetch_http_client());
    let app = Application::with_platform(platform)
        .with_assets(MobileAssets)
        .with_http_client(client);
    let handle = app.run_embedded(|cx| {
        gpui_kit::init(cx);
        crate::design::install(cx);
        gpui_kit::component::Theme::update(cx, |theme| {
            theme.font_family = "IBM Plex Sans".into();
            theme.font_size = px(16.);
        });
        cx.text_system()
            .add_fonts(vec![Cow::Borrowed(
                include_bytes!("../fonts/IBMPlexSans-Regular.ttf").as_slice(),
            )])
            .expect("bundled font");
        let (_, content) = gpui_kit::open_window(WindowOptions::default(), cx, |window, cx| {
            let search = cx.new(|cx| InputState::new(window, cx));
            let composer = cx.new(|cx| TextareaState::new(window, cx));
            cx.new(|_| Mobile {
                data: json!({}),
                page: "chat".into(),
                composer,
                search,
                drafts: pot_ui::state::Drafts::default(),
                expanded: HashSet::new(),
                conversation: "main".into(),
                accepted: String::new(),
            })
        })
        .expect("mobile window");
        VIEW.with(|view| *view.borrow_mut() = Some(content));
    });
    HANDLE.with(|cell| *cell.borrow_mut() = Some(handle));
}

// JS owns fetch/cookies/browser history. Rust owns command identity and receipts.
#[wasm_bindgen]
pub fn begin_send(conversation: &str, text: &str, request_id: &str) -> Result<String, JsValue> {
    SEND.with(|state| {
        let request = state
            .borrow_mut()
            .begin(request_id.into(), conversation.into(), text.into())
            .map_err(JsValue::from_str)?;
        serde_json::to_string(&request).map_err(|e| JsValue::from_str(&e.to_string()))
    })
}
#[wasm_bindgen]
pub fn pending_send() -> String {
    SEND.with(|state| serde_json::to_string(&state.borrow().pending()).unwrap())
}
#[wasm_bindgen]
pub fn accept_send(request_id: &str, response: &str) -> Result<String, JsValue> {
    let _: pot_protocol::SendMessageAccepted =
        serde_json::from_str(response).map_err(|_| JsValue::from_str("invalid_send_response"))?;
    SEND.with(|state| Ok(serde_json::to_string(&state.borrow_mut().accept(request_id)).unwrap()))
}
#[wasm_bindgen]
pub fn reconcile_send(snapshot: &str) -> String {
    let Ok(snapshot) = serde_json::from_str::<pot_protocol::Snapshot>(snapshot) else {
        return "null".into();
    };
    SEND.with(|state| serde_json::to_string(&state.borrow_mut().reconcile(&snapshot)).unwrap())
}
