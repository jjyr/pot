use gpui_kit::component::{
    ActiveTheme, Disableable,
    button::{Button, ButtonVariants},
    input::{InputState, Textarea, TextareaState},
};
use gpui_kit::{prelude::*, *};
use serde_json::{Value, json};
use std::{borrow::Cow, cell::RefCell, collections::HashSet, rc::Rc, sync::Arc};
use wasm_bindgen::prelude::*;
gpui_kit::assets::icon_assets!(
    MobileAssets,
    [
        Copy,
        Check,
        ExternalLink,
        Inbox,
        Globe,
        Settings,
        Ellipsis,
        RefreshCw,
        Info,
        ChartPie
    ]
);
thread_local! {
    static SEND: RefCell<pot_ui::state::SendState> = RefCell::new(pot_ui::state::SendState::default());
    static VIEW: RefCell<Option<Entity<Mobile>>> = const { RefCell::new(None) };
    static HANDLE: RefCell<Option<ApplicationHandle>> = const { RefCell::new(None) };
}
#[wasm_bindgen]
extern "C" {
    #[wasm_bindgen(js_namespace = window, js_name = potAction)]
    fn action(name: &str, value: &str);
    #[wasm_bindgen(js_namespace = window, js_name = potIsComposing)]
    fn is_composing() -> bool;
}
struct Mobile {
    assistants_ui: pot_ui::assistants::Editor,
    machine_connections: pot_ui::machine_connections::Editor,
    assistant_model_open: bool,
    assistant_save_sequence: u64,
    brand_icon: Arc<Image>,
    data: Value,
    page: String,
    composer: Entity<TextareaState>,
    composer_watch: Subscription,
    completion_index: usize,
    completion_context: (String, usize),
    completion_dismissed: Option<(String, usize)>,
    drafts: pot_ui::state::Drafts<Entity<TextareaState>>,
    expanded: HashSet<String>,
    conversation: String,
    accepted: String,
    tasks_scroll: ScrollHandle,
    usage_ui: pot_ui::usage::UsageUI,
    machine_selection: Value,
    locale: String,
    search: Entity<InputState>,
}
impl Mobile {
    fn catalog(&self) -> pot_ui::localization::Catalog {
        pot_ui::localization::Catalog::new(self.data["locale"].as_str().unwrap_or("en"))
    }
    fn page_handler(&self, cx: &Context<Self>) -> pot_ui::pages::Handler {
        Rc::new(
            cx.listener(|this, event: &pot_ui::pages::PageAction, window, cx| {
                let Some(event) = this.usage_ui.apply(event, this.catalog(), cx) else {
                    action(
                        "usage-query",
                        &json!({"days":this.usage_ui.days,"offset":this.usage_ui.group_offset})
                            .to_string(),
                    );
                    cx.notify();
                    return;
                };
                match event.kind {
                    "assistant-edit" => this
                        .assistants_ui
                        .select(event.value.as_str().unwrap_or("main")),
                    "assistant-new" => this.assistants_ui.select("new"),
                    "assistant-reload" => this
                        .assistants_ui
                        .reload(event.value.as_str().unwrap_or("main")),
                    "assistant-close" => this.assistants_ui.open = false,
                    "assistant-select" => {
                        this.assistants_ui
                            .select(event.value.as_str().unwrap_or("main"));
                        this.assistant_model_open = false;
                        action("assistant-select", event.value.as_str().unwrap_or("main"));
                    }
                    "machine-connections-toggle" => {
                        this.machine_connections.open = !this.machine_connections.open
                    }
                    "machine-command" => action("machine-command", &event.value.to_string()),
                    "assistant-save" => action("assistant-save", &event.value.to_string()),
                    "assistant-model" => this.assistant_model_open = !this.assistant_model_open,
                    "assistant-profile" => action(
                        "assistant-profile",
                        event.value.as_str().unwrap_or_default(),
                    ),
                    "task-filter" | "task-clear-search" | "task-previous" | "task-next" => {
                        let query = serde_json::from_value::<pot_protocol::TaskQuery>(
                            this.data["task_query"].clone(),
                        )
                        .unwrap_or_default();
                        let page = serde_json::from_value::<pot_protocol::TaskPage>(
                            this.data["task_page"].clone(),
                        )
                        .unwrap_or_default();
                        if let Some(query) =
                            pot_ui::page_state::task_query(&event.kind, &event.value, &query, &page)
                        {
                            if event.kind == "task-clear-search" {
                                this.search.update(cx, |f, cx| f.set_value("", window, cx));
                            }
                            action("task-query", &serde_json::to_string(&query).unwrap());
                        }
                    }
                    "permission-decision" => {
                        action("permission-decision", &event.value.to_string())
                    }
                    "task-open" => action("task", &event.value.to_string()),
                    "execution-target-filter-clear" => action("execution-target-filter-clear", ""),
                    "execution-target-tasks" => {
                        action("execution-target-tasks", &event.value.to_string())
                    }
                    "machine-select" => {
                        this.machine_selection = event.value.clone();
                    }
                    "refresh" => action("refresh", ""),
                    "usage-page" => {
                        if event.value == 4 {
                            action("page", "usage");
                        }
                    }
                    _ => {}
                }
                cx.notify();
            }),
        )
    }

    fn completions(&self, cx: &App) -> Vec<pot_protocol::references::Completion> {
        if self.page != "chat" || is_composing() {
            return vec![];
        }
        let input = self.composer.read(cx);
        let text = input.value().to_string();
        let caret = input.cursor();
        if !input.selected_range().is_empty()
            || self.completion_dismissed.as_ref() == Some(&(text.clone(), caret))
        {
            return vec![];
        }
        let Some((_, query)) = pot_protocol::references::query(&text, caret) else {
            return vec![];
        };
        serde_json::from_value::<Vec<pot_protocol::references::Completion>>(
            self.data["completions"]["items"].clone(),
        )
        .unwrap_or_default()
        .into_iter()
        .filter(|i| pot_protocol::references::matches(i, query))
        .take(30)
        .collect()
    }
    fn completion_key(&mut self, key: &str, window: &mut Window, cx: &mut Context<Self>) {
        let items = self.completions(cx);
        if items.is_empty() {
            return;
        }
        match key {
            "down" => self.completion_index = (self.completion_index + 1) % items.len(),
            "up" => {
                self.completion_index =
                    (self.completion_index.min(items.len() - 1) + items.len() - 1) % items.len()
            }
            "enter" => {
                let token = items[self.completion_index.min(items.len() - 1)]
                    .token
                    .clone();
                self.insert_completion(&token, window, cx);
            }
            "escape" => {
                let input = self.composer.read(cx);
                self.completion_dismissed = Some((input.value().to_string(), input.cursor()));
            }
            _ => return,
        }
        cx.stop_propagation();
        window.prevent_default();
        cx.notify();
    }
    fn insert_completion(&mut self, token: &str, window: &mut Window, cx: &mut Context<Self>) {
        self.composer.update(cx, |input, cx| {
            let text = input.value().to_string();
            let caret = input.cursor();
            if let Some((start, _)) = pot_protocol::references::query(&text, caret) {
                input.set_selected_range(start..caret, cx);
                input.replace(format!("{token} "), window, cx);
                input.focus(window, cx);
            }
        });
        self.completion_index = 0;
        self.completion_dismissed = None;
    }
    fn completion_menu(&self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let mut menu = div()
            .flex()
            .flex_col()
            .bg(crate::design::navigation(cx))
            .rounded_lg();
        let items = self.completions(cx);
        let rows = ((f32::from(window.fully_visible_bounds().size.height) - 230.) / 40.)
            .clamp(1., 6.) as usize;
        // Bound the popup to the visible viewport, including a raised phone keyboard.
        let selected = self.completion_index.min(items.len().saturating_sub(1));
        for (index, item) in items
            .into_iter()
            .enumerate()
            .skip(selected.saturating_sub(rows - 1))
            .take(rows)
        {
            let token = item.token.clone();
            menu = menu.child(
                Button::new(SharedString::from(format!("completion-{index}")))
                    .ghost()
                    .h(px(40.))
                    .label(format!(
                        "{} {} · {}",
                        if index == selected { "›" } else { " " },
                        item.label,
                        item.token.split(':').next().unwrap_or("")
                    ))
                    .on_click(cx.listener(move |this, _, window, cx| {
                        this.insert_completion(&token, window, cx);
                        cx.notify();
                    })),
            );
        }
        menu
    }
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
        let save_sequence = self.data["assistant_save_sequence"].as_u64().unwrap_or(0);
        if save_sequence != self.assistant_save_sequence {
            if let Some(owner) = self.data["assistant_saved_owner"].as_str() {
                self.assistants_ui.saved(owner);
            }
            self.assistant_save_sequence = save_sequence;
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
            self.composer_watch = cx.observe(&self.composer, |_, _, cx| cx.notify());
            self.completion_dismissed = None;
            self.completion_index = 0;
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
        let input = self.composer.read(cx);
        let context = (input.value().to_string(), input.cursor());
        if context != self.completion_context {
            self.completion_index = 0;
            self.completion_context = context;
        }
        let locale = self.data["locale"].as_str().unwrap_or("en").to_owned();
        if locale != self.locale {
            let catalog = self.catalog();
            self.search.update(cx, |input, cx| {
                input.set_placeholder(catalog.text("main.search-task-title-or-id-47"), window, cx)
            });
            self.usage_ui.localize(catalog, window, cx);
            self.locale = locale;
        }
        cx.set_reduce_motion(self.data["reduce_motion"] == true);
        let visible = window.fully_visible_bounds();
        let compact = visible.size.width < px(900.);
        let sidebar = if compact { 0. } else { 176. };
        let column_width = (f32::from(visible.size.width) - sidebar).clamp(0., 820.);
        cx.set_global(crate::design::Viewport(px(column_width)));
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
            .w_full()
            .max_w(px(820.))
            .mx_auto()
            .min_w_0()
            .flex_1()
            .min_h_0()
            .overflow_y_scroll()
            .p_4()
            .flex()
            .flex_col()
            .gap_3();
        if self.page == "chat" {
            let handler = self.page_handler(cx);
            let current = self.data["assistant_id"].as_str().unwrap_or("main");
            content = content.child(self.assistants_ui.render(
                pot_ui::assistants::View {
                    assistants: &self.data["assistants"],
                    current,
                    rules: &self.data["assistant_rules"],
                },
                self.catalog(),
                handler.clone(),
                window,
                cx,
            ));
            if self.assistant_model_open {
                content = content.child(
                    div().flex().flex_col().gap_2().children(
                        self.data["assistant_configuration"]["profiles"]
                            .as_array()
                            .into_iter()
                            .flatten()
                            .map(|profile| {
                                Button::new(SharedString::from(format!(
                                    "assistant-profile-{}",
                                    profile["id"].as_str().unwrap_or_default()
                                )))
                                .ghost()
                                .label(format!(
                                    "{} · {}",
                                    profile["name"].as_str().unwrap_or_default(),
                                    profile["model"].as_str().unwrap_or_default()
                                ))
                                .on_click(pot_ui::pages::on(
                                    &handler,
                                    "assistant-profile",
                                    profile["id"].clone(),
                                ))
                            }),
                    ),
                );
            }
        }
        if self.page == "chat" || self.page == "task" {
            content = content.gap_5().child(pot_ui::permissions::cards(
                &self.data["permissions"],
                &self.data["machines"],
                &self.conversation,
                self.catalog(),
                self.page_handler(cx),
                cx,
            ));
            if let Some(snapshot) = &snapshot {
                let labels = pot_ui::chat::Labels::new(|key| self.label(key));
                let handler: pot_ui::chat::ChatHandler = Arc::new(cx.listener(
                    |this, event: &pot_ui::chat::ChatAction, _, cx| {
                        match event {
                            pot_ui::chat::ChatAction::ToggleTool(id) => {
                                if !this.expanded.remove(id) {
                                    this.expanded.insert(id.clone());
                                }
                            }
                            pot_ui::chat::ChatAction::OpenTask(id) => action("task", id),
                            pot_ui::chat::ChatAction::DownloadFile(link) => {
                                if let Some((task, path)) = pot_protocol::parse_file_link(link) {
                                    action(
                                        "file-download",
                                        &serde_json::json!({"task_id":task,"path":path})
                                            .to_string(),
                                    );
                                }
                            }
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
                                touch: compact,
                                available_width: column_width - 32.,
                            },
                        },
                        handler.clone(),
                        cx,
                    ));
                }
            }
        } else if self.page == "tasks" {
            let page =
                serde_json::from_value::<pot_protocol::TaskPage>(self.data["task_page"].clone())
                    .unwrap_or_default();
            let query =
                serde_json::from_value::<pot_protocol::TaskQuery>(self.data["task_query"].clone())
                    .unwrap_or_default();
            let navigation = div().flex().gap_2().child(
                Button::new("task-search-submit")
                    .ghost()
                    .h(px(44.))
                    .label(self.label("mobile.search"))
                    .on_click(cx.listener(|this, _, _, cx| {
                        action("search", &this.search.read(cx).value())
                    })),
            );
            content = content.p_0().gap_0().child(pot_ui::tasks::render(
                pot_ui::tasks::TasksView {
                    local_runtime: self.data["runtime"]["runtime_id"]
                        .as_str()
                        .unwrap_or_default(),
                    assistant_names: &self.data["assistants"],
                    search: &self.search,
                    scroll: &self.tasks_scroll,
                    navigation,
                    tasks: &page.items,
                    task_page: &page,
                    task_query: &query,
                    tasks_loading: false,
                    connected: self.data["connected"] == true,
                    error: self.data["page_error"].as_str().unwrap_or(""),
                    parallel: None,
                },
                pot_ui::pages::Layout {
                    native: false,
                    compact,
                },
                self.catalog(),
                self.page_handler(cx),
                window,
                cx,
            ));
        } else if self.page == "machines" {
            let catalog = self.catalog();
            let handler = self.page_handler(cx);
            let connections = self.machine_connections.render(
                pot_ui::machine_connections::View {
                    client: None,
                    directory: &self.data["machines"],
                    selected: &self.machine_selection,
                    reply: &self.data["machine_reply"],
                    pending: self.data["machine_pending"] == true,
                },
                catalog,
                handler.clone(),
                window,
                cx,
            );
            content = content
                .p_0()
                .gap_0()
                .child(connections)
                .child(pot_ui::machines::render(
                    pot_ui::machines::MachinesView {
                        permissions: &self.data["permissions"],
                        directory: &self.data["machines"],
                        preferred: &self.machine_selection,
                        desktop_runtime: None,
                    },
                    pot_ui::pages::Layout {
                        native: false,
                        compact,
                    },
                    self.catalog(),
                    self.page_handler(cx),
                    cx,
                ));
        } else if self.page == "usage" {
            let handler = self.page_handler(cx);
            content = content.p_0().gap_0().child(self.usage_ui.render(
                pot_ui::usage::UsageView {
                    page: 4,
                    usage: &self.data["usage"],
                    usage_error: self.data["page_error"].as_str().unwrap_or(""),
                    usage_notice: "",
                    usage_pending: false,
                    selected_title: None,
                    controls: false,
                },
                pot_ui::pages::Layout {
                    native: false,
                    compact,
                },
                self.catalog(),
                handler,
                window,
                cx,
            ));
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
                                ))
                                .child(div().child(format!(
                                        "{} · {}",
                                        item["spec"]["execution_target"]["runtime_id"]
                                            .as_str()
                                            .unwrap_or_default(),
                                        match item["spec"]["execution_target"]["target"]["kind"]
                                            .as_str()
                                        {
                                            Some("host") => "Host".to_owned(),
                                            Some("sandbox") => format!(
                                            "Sandbox / {}",
                                            item["spec"]["execution_target"]["target"]["sandbox_id"]
                                                .as_str()
                                                .unwrap_or_default()
                                        ),
                                            _ => String::new(),
                                        }
                                    ))),
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
                .child(self.label("mobile.owner-access-to-this-pot-instance-and-its-sandboxs"))
                .child(
                    Button::new("language")
                        .h(px(44.))
                        .label(self.label("mobile.language"))
                        .on_click(|_, _, _| action("language", "")),
                )
                .child(
                    Button::new("configure-token")
                        .h(px(44.))
                        .label(self.label("web.change-token"))
                        .on_click(|_, _, _| action("configure-token", "")),
                )
                .child(
                    Button::new("logout")
                        .h(px(44.))
                        .label(self.label("mobile.sign-out"))
                        .on_click(|_, _, _| action("logout", "")),
                );
        }
        let mut main = div()
            .h_full()
            .flex_1()
            .min_w_0()
            .min_h_0()
            .bg(cx.theme().background)
            .text_color(cx.theme().foreground)
            .flex()
            .flex_col()
            .child(
                div()
                    .h(px(48.))
                    .flex_shrink_0()
                    .px_3()
                    .flex()
                    .items_center()
                    .gap_2()
                    .child(img(self.brand_icon.clone()).size(px(28.)))
                    .child("Pot")
                    .when(self.page == "task", |el| {
                        el.child(
                            Button::new("back-to-tasks")
                                .ghost()
                                .h(px(44.))
                                .label(self.label("main.back-to-tasks"))
                                .on_click(|_, _, _| action("page", "tasks")),
                        )
                    }),
            )
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
                    .w_full()
                    .max_w(px(820.))
                    .mx_auto()
                    .flex_shrink_0()
                    .p_3()
                    .flex()
                    .flex_col()
                    .gap_2()
                    .capture_action(cx.listener(
                        |this, _: &gpui_kit::component::input::MoveDown, window, cx| {
                            this.completion_key("down", window, cx);
                        },
                    ))
                    .capture_action(cx.listener(
                        |this, _: &gpui_kit::component::input::MoveUp, window, cx| {
                            this.completion_key("up", window, cx);
                        },
                    ))
                    .capture_action(cx.listener(
                        |this, event: &gpui_kit::component::input::Enter, window, cx| {
                            if !event.shift && !event.secondary {
                                this.completion_key("enter", window, cx);
                            }
                        },
                    ))
                    .capture_action(cx.listener(
                        |this, _: &gpui_kit::component::input::Escape, window, cx| {
                            this.completion_key("escape", window, cx);
                        },
                    ))
                    .child(self.completion_menu(window, cx))
                    .child(
                        Textarea::new(&self.composer)
                            .aria_label(self.label("mobile.message"))
                            .h(px(if visible.size.height < px(500.) {
                                48.
                            } else {
                                80.
                            })),
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
            .id("web-navigation")
            .flex_shrink_0()
            .flex()
            .gap_1()
            .bg(crate::design::navigation(cx))
            .border_color(cx.theme().border)
            .when(compact, |el| {
                el.h(px(56.))
                    .items_center()
                    .justify_between()
                    .px_2()
                    .border_t_1()
            })
            .when(!compact, |el| {
                el.w(px(sidebar)).h_full().flex_col().p_3().border_r_1()
            });
        for (name, key) in [
            ("chat", "mobile.chat"),
            ("tasks", "mobile.tasks"),
            ("machines", "machines.title"),
            ("usage", "mobile.usage"),
            ("schedules", "mobile.schedules"),
            ("more", "mobile.more"),
        ] {
            if compact && matches!(name, "usage" | "schedules") {
                continue;
            }
            let selected = self.page == name
                || (name == "tasks" && self.page == "task")
                || (compact
                    && name == "more"
                    && matches!(self.page.as_str(), "usage" | "schedules"));
            nav = nav.child(
                Button::new(name)
                    .h(px(44.))
                    .when(!compact, |el| el.w_full())
                    .ghost()
                    .when(selected, |el| el.primary())
                    .label(self.label(key))
                    .on_click(move |_, _, _| action("page", name)),
            );
        }
        let shell = div()
            .absolute()
            .left(visible.origin.x)
            .top(visible.origin.y)
            .w(visible.size.width)
            .h(visible.size.height)
            .overflow_hidden()
            .flex()
            .bg(cx.theme().background);
        if compact {
            shell.flex_col().child(main).child(nav)
        } else {
            shell.child(nav).child(main)
        }
    }
}
#[wasm_bindgen]
pub fn update_view(data: &str) {
    let Ok(mut data) = serde_json::from_str::<Value>(data) else {
        return;
    };
    HANDLE.with(|cell| {
        if let Some(app) = cell.borrow().as_ref() {
            VIEW.with(|view| {
                if let Some(view) = view.borrow().as_ref() {
                    app.update(|cx| {
                        view.update(cx, |this, cx| {
                            if let Some(machines) = data["machines"]["machines"].as_array_mut() {
                                for machine in machines {
                                    if machine["local"] == true {
                                        machine["name"] = serde_json::json!(
                                            this.catalog().text("machines.control")
                                        );
                                    }
                                }
                            }
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
            cx.new(|cx| Mobile {
                assistants_ui: Default::default(),
                machine_connections: Default::default(),
                assistant_model_open: false,
                assistant_save_sequence: 0,
                brand_icon: Arc::new(Image::from_bytes(
                    ImageFormat::Png,
                    include_bytes!("../../app/assets/Pot128.png").to_vec(),
                )),
                data: json!({}),
                page: "chat".into(),
                composer_watch: cx.observe(&composer, |_, _, cx| cx.notify()),
                completion_index: 0,
                completion_context: Default::default(),
                completion_dismissed: None,
                composer,
                search,
                drafts: pot_ui::state::Drafts::default(),
                expanded: HashSet::new(),
                conversation: "main".into(),
                accepted: String::new(),
                tasks_scroll: ScrollHandle::new(),
                usage_ui: pot_ui::usage::UsageUI::new(
                    pot_ui::localization::Catalog::new("en"),
                    window,
                    cx,
                ),
                machine_selection: Value::Null,
                locale: String::new(),
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
