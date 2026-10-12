extern crate gpui_kit as gpui;

rust_i18n::i18n!("locales", fallback = "en");
mod artifacts;
mod backend;
mod client;
use pot_ui::design;
mod i18n;

mod navigation;
mod schedules_ui;
#[cfg(test)]
mod stream;
mod ui;
mod usage_ui;

use gpui::{div, prelude::*, px, rgb, *};
use gpui_kit::component::{
    ActiveTheme, Icon, IconName, Sizable, Theme, ThemeMode,
    button::{Button, ButtonVariants},
    input::InputState,
};
use pot_protocol::Role;
use std::{
    collections::HashSet,
    ffi::{CStr, CString},
};

unsafe extern "C" {
    fn pot_bridge_startup_error(message: *const std::ffi::c_char);
    fn pot_bridge_set_wake(callback: extern "C" fn());
    fn pot_bridge_install(html: *const std::ffi::c_char);
    fn pot_bridge_hide();
    fn pot_bridge_update(json: *const std::ffi::c_char);
    fn pot_bridge_event(buffer: *mut std::ffi::c_char, capacity: i32) -> i32;
    fn pot_bridge_ui(buffer: *mut std::ffi::c_char, capacity: i32) -> i32;
    fn pot_bridge_command(json: *const std::ffi::c_char);
    fn pot_bridge_chinese() -> bool;
    fn pot_bridge_dark() -> bool;
    fn pot_bridge_reduce_motion() -> bool;
    fn pot_bridge_composer_height() -> f64;
}
static UI_WAKE: std::sync::OnceLock<async_channel::Sender<()>> = std::sync::OnceLock::new();
extern "C" fn wake_ui() {
    if let Some(sender) = UI_WAKE.get() {
        let _ = sender.try_send(());
    }
}
struct ModeCache {
    client: client::Client,
    navigation: navigation::Navigation<navigation::State>,
    assistants_ui: pot_ui::assistants::Editor,
    machine_connections: pot_ui::machine_connections::Editor,
    settings: ui::Settings,
    schedules_ui: schedules_ui::SchedulesUI,
    usage_ui: usage_ui::UsageUI,
    machine_selection: serde_json::Value,
}
struct Shell {
    other_mode: Option<ModeCache>,
    pending_mode: bool,
    client: client::Client,
    scroll: ScrollHandle,
    tasks_scroll: ScrollHandle,
    navigation: navigation::Navigation<navigation::State>,
    pending_up: bool,
    pending_offsets: Option<[Point<Pixels>; 5]>,
    pending_task_anchor: Option<(String, f32)>,
    last_content: String,
    summary_count: usize,
    summary_removal: Option<(std::time::Instant, usize)>,
    expanded: HashSet<String>,
    first: bool,
    show_tasks: bool,
    last_view: Option<client::View>,
    last_dark: bool,
    last_height: f64,
    last_at_bottom: bool,
    jumping_to_latest: bool,
    native: serde_json::Value,
    settings: ui::Settings,
    task_search: Entity<InputState>,
    assistants_ui: pot_ui::assistants::Editor,
    machine_connections: pot_ui::machine_connections::Editor,
    machine_selection: serde_json::Value,
    schedule_tab: bool,
    schedules_ui: schedules_ui::SchedulesUI,
    usage_ui: usage_ui::UsageUI,
}
impl Shell {
    fn switch_mode(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if backend::toggle_mode().is_err() {
            self.machine_connections.open = true;
            ui::command(serde_json::json!({"action":"page","value":1}));
            return;
        }
        let mut other = self.other_mode.take().unwrap_or_else(|| ModeCache {
            client: client::Client::start_mode(backend::mode()),
            navigation: Default::default(),
            assistants_ui: Default::default(),
            machine_connections: Default::default(),
            settings: ui::Settings::new(window, cx),
            schedules_ui: schedules_ui::SchedulesUI::new(window, cx),
            usage_ui: usage_ui::UsageUI::new(self.catalog(), window, cx),
            machine_selection: serde_json::Value::Null,
        });
        std::mem::swap(&mut self.client, &mut other.client);
        std::mem::swap(&mut self.navigation, &mut other.navigation);
        std::mem::swap(&mut self.assistants_ui, &mut other.assistants_ui);
        std::mem::swap(
            &mut self.machine_connections,
            &mut other.machine_connections,
        );
        std::mem::swap(&mut self.settings, &mut other.settings);
        std::mem::swap(&mut self.schedules_ui, &mut other.schedules_ui);
        std::mem::swap(&mut self.usage_ui, &mut other.usage_ui);
        std::mem::swap(&mut self.machine_selection, &mut other.machine_selection);
        self.other_mode = Some(other);
        backend::select_assistant(self.client.view.lock().unwrap().assistant_id());
        self.pending_up = false;
        self.pending_offsets = None;
        self.pending_task_anchor = None;
        self.last_view = None;
        self.last_content.clear();
        self.expanded.clear();
        self.first = true;
        self.show_tasks = false;
        self.schedule_tab = false;
        self.task_search
            .update(cx, |f, cx| f.set_value("", window, cx));
        ui::command(serde_json::json!({"action":"mode-changed","mode":backend::mode()}));
        self.client.refresh();
    }
    fn catalog(&self) -> pot_ui::localization::Catalog {
        pot_ui::localization::Catalog::new(&rust_i18n::locale())
    }
    fn page_handler(&self, cx: &Context<Self>) -> pot_ui::pages::Handler {
        std::rc::Rc::new(
            cx.listener(|this, event: &pot_ui::pages::PageAction, window, cx| {
                let Some(event) = this.usage_ui.apply(event, this.catalog(), cx) else {
                    cx.notify();
                    return;
                };
                match event.kind {
                    "assistant-edit" => this.assistants_ui.select(event.value.as_str().unwrap_or("main")),
                    "assistant-new" => this.assistants_ui.select("new"),
                    "assistant-select" => { this.client.select_assistant(event.value.as_str().unwrap_or("main").into()); this.assistants_ui.select(event.value.as_str().unwrap_or("main")); },
                    "assistant-reload" => this.assistants_ui.reload(event.value.as_str().unwrap_or("main")),
                    "assistant-close" => this.assistants_ui.open = false,
                    "machine-connections-toggle" => this.machine_connections.open = !this.machine_connections.open,
                    "machine-command" => this.client.machine_command(event.value),
                    "control-chat" => this.pending_mode = backend::mode() == "local",
                    "assistant-save" => this.client.assistant_save(event.value),
                    "assistant-model" => ui::command(serde_json::json!({"action":"ai-open","owner":event.value,"return_to_chat":true})),
                    "permission-decision" => this.client.permission_decision(event.value),
                    "native" => ui::command(event.value),
                    "machine-select" => {
                        this.machine_selection = event.value.clone();
                        ui::command(serde_json::json!({"action":"machines-detail","value":true}));
                    }
                    "environment-tasks" => {
                        this.client.filter_environment(event.value.to_string());
                        this.show_tasks = true;
                        ui::command(serde_json::json!({"action":"page","value":3}));
                        this.client.refresh();
                    }
                    "environment-filter-clear" => {
                        this.client.filter_environment(String::new());
                        this.client.refresh();
                    }
                    "environment-desktop" => {
                        ui::command(serde_json::json!({"action":"environment-desktop","id":event.value["target"]["sandbox_id"]}));
                    }
                    "sandbox-select" => {
                        ui::command(serde_json::json!({"action":"sandbox-view","id":event.value}))
                    }
                    "refresh" => this.client.refresh(),
                    "task-filter" | "task-clear-search" | "task-previous" | "task-next" => {
                        let v = this.client.view.lock().unwrap().clone();
                        let Some(q) = pot_ui::page_state::task_query(
                            event.kind,
                            &event.value,
                            &v.task_query,
                            &v.task_page,
                        ) else {
                            return;
                        };
                        if event.kind == "task-clear-search" {
                            this.task_search
                                .update(cx, |f, cx| f.set_value("", window, cx));
                        }
                        this.client.filter_tasks(q.scope, q.q, q.offset);
                    }
                    "task-open" => {
                        this.show_tasks = false;
                        this.client
                            .select(event.value["id"].as_str().or(event.value.as_str()).unwrap_or("").into());
                        this.first = true;
                    }
                    "usage-page" => {
                        ui::command(serde_json::json!({"action":"page","value":event.value}))
                    }
                    "usage-policy" => this.client.usage_action("policy", event.value),
                    "usage-resume" => this.client.usage_action("resume", event.value),
                    "usage-cancel" => {
                        if this.client.view.lock().unwrap().selected.is_empty() {
                            this.client.usage_action("cancel", serde_json::json!({}));
                        } else {
                            this.client.cancel();
                        }
                    }
                    _ => {}
                }
                cx.notify();
            }),
        )
    }

    fn tick(&mut self, cx: &mut Context<Self>) -> bool {
        if let Some(owner) = self
            .client
            .view
            .lock()
            .unwrap()
            .assistant_saved_owner
            .take()
        {
            self.assistants_ui.saved(&owner);
        }
        let old_tasks = self.show_tasks;
        {
            let mut v = self.client.view.lock().unwrap();
            v.schedules_visible = self.show_tasks && self.schedule_tab;
        }
        let success = self.client.view.lock().unwrap().schedule_success;
        if success != self.schedules_ui.success {
            self.schedules_ui.success = success;
            self.schedules_ui.open = false;
        }
        let search = self.task_search.read(cx).value().to_string();
        let query = self.client.view.lock().unwrap().task_query.clone();
        if search != query.q {
            self.client.filter_tasks(query.scope, search, 0);
        }
        let mut buffer = vec![0i8; 524288];
        while unsafe { pot_bridge_event(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
            let event: serde_json::Value =
                serde_json::from_slice(unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes())
                    .unwrap_or_default();
            match event["action"].as_str() {
                Some("mode-toggle") => self.pending_mode = true,
                Some("send") => {
                    if let Some(text) = event["text"].as_str() {
                        self.client.send(text.into());
                    }
                }
                Some("copy-selection") => cx.dispatch_action(&gpui_kit::base::input::Copy),
                Some("select-all") => cx.dispatch_action(&gpui_kit::base::input::SelectAll),
                Some("paste") => cx.dispatch_action(&gpui_kit::base::input::Paste),
                Some("cut") => cx.dispatch_action(&gpui_kit::base::input::Cut),
                Some("undo") => cx.dispatch_action(&gpui_kit::base::input::Undo),
                Some("redo") => cx.dispatch_action(&gpui_kit::base::input::Redo),
                Some("cancel") => self.client.cancel(),
                Some("navigate-up") => self.pending_up = true,
                Some("tasks") => {
                    self.show_tasks = true;
                }
                Some("main") => {
                    self.show_tasks = false;
                    self.client.select(String::new());
                    self.first = true;
                }
                _ => {}
            }
        }
        let native = if unsafe { pot_bridge_ui(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
            serde_json::from_slice(unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes())
                .unwrap_or_default()
        } else {
            serde_json::Value::Null
        };
        let native_changed = native != self.native;
        let language_changed = native["settings"]["locale"]
            .as_str()
            .is_some_and(crate::i18n::set_locale);
        self.native = native;
        if language_changed {
            cx.notify();
        }
        {
            let mut v = self.client.view.lock().unwrap();
            let page = self.native["page"].as_u64().unwrap_or(0);
            let scope = if page == 4 {
                String::new()
            } else if v.selected.is_empty() {
                v.assistant_id().to_owned()
            } else {
                v.selected.clone()
            };
            let timezone = self.native["timezone"]
                .as_str()
                .unwrap_or("Asia/Shanghai")
                .to_string();
            let context = (scope.clone(), self.usage_ui.days, timezone.clone());
            if self.usage_ui.group_context.as_ref() != Some(&context) {
                self.usage_ui.group_offset = 0;
                self.usage_ui.group_context = Some(context);
            }
            let query = if [0, 3, 4, 5, 6].contains(&page) {
                Some((
                    scope,
                    self.usage_ui.days,
                    if [4, 5].contains(&page) {
                        self.usage_ui.group_offset
                    } else {
                        0
                    },
                    timezone,
                ))
            } else {
                None
            };
            if v.usage_query != query {
                v.usage = serde_json::Value::Null;
                v.usage_error.clear();
            }
            v.usage_query = query;
        }
        let (accepted, accepted_conversation) = {
            let mut view = self.client.view.lock().unwrap();
            (view.accepted_text.take(), view.accepted_conversation.take())
        };
        let view = self.client.view.lock().unwrap().clone();
        let dark = unsafe { pot_bridge_dark() };
        cx.set_reduce_motion(unsafe { pot_bridge_reduce_motion() });
        let height = unsafe { pot_bridge_composer_height() };
        let summary_count = view.running_tasks.len();
        let summary_changed = summary_count != self.summary_count;
        if summary_count < self.summary_count && !cx.reduce_motion() {
            self.summary_removal = Some((
                std::time::Instant::now(),
                self.summary_count - summary_count,
            ));
        }
        self.summary_count = summary_count;
        let removal_finished = self
            .summary_removal
            .is_some_and(|(start, _)| start.elapsed().as_millis() >= 220);
        if removal_finished || cx.reduce_motion() {
            self.summary_removal = None;
        }
        let mut changed = self.pending_mode
            || summary_changed
            || removal_finished
            || native_changed
            || self.last_view.as_ref() != Some(&view)
            || old_tasks != self.show_tasks
            || dark != self.last_dark
            || height != self.last_height;
        self.last_view = Some(view.clone());
        self.last_dark = dark;
        self.last_height = height;
        let snapshot = view.snapshot.as_ref();
        let running = snapshot.is_some_and(|s| s.turn.is_some());
        let messages = if view.selected == "new" {
            vec![]
        } else {
            snapshot.map(|s| s.messages.clone()).unwrap_or_default()
        };
        let at_bottom = self.scroll.max_offset().y + self.scroll.offset().y <= px(32.);
        // Scrolling changes the handle during layout, after render. Observe it
        // independently of Runtime snapshots so hover is not required to redraw.
        changed |= at_bottom != self.last_at_bottom;
        self.last_at_bottom = at_bottom;
        if at_bottom {
            self.jumping_to_latest = false;
        }

        // Include streaming text changes, not just durable event cursors.
        let content = messages
            .iter()
            .map(|m| format!("{}:{}:{:?}", m.id, m.text.len(), m.status))
            .collect::<Vec<_>>()
            .join(";");
        if content != self.last_content && (at_bottom || self.first || accepted.is_some()) {
            self.scroll.scroll_to_bottom();
        }
        if !messages.is_empty() {
            self.first = false;
        }
        self.last_content = content;
        let state = serde_json::json!({
            "completions": if view.selected.is_empty() { view.completions.clone() } else { serde_json::Value::Null },
            "can_back":self.route(&view).parent().is_some(),"boot_id":snapshot.map(|s|s.boot_id.as_str()),"has_snapshot":snapshot.is_some(),"running":view.is_cooking(),"selected_running":running,"selected":view.selected,"assistant_id":view.assistant_id(),"client_mode":view.client_mode,"conversation":format!("{}/{}",view.client_mode,if view.selected.is_empty() {view.assistant_id()} else {&view.selected}),"task_list":self.show_tasks,"can_send":view.can_send(),"connected":view.error.is_empty() && snapshot.is_some(),
            "needs_configuration":snapshot.is_some_and(|s|s.agent.phase == pot_protocol::AgentPhase::NeedsConfiguration),
            "error":snapshot.is_some_and(|s|s.agent.phase == pot_protocol::AgentPhase::Failed),
            "model":snapshot.map(|s|s.agent.model.as_str()).unwrap_or(crate::i18n::text("main.connecting")),
            "used":snapshot.and_then(|s|s.budget.as_ref()).map(|b|b.used),
            "limit":snapshot.and_then(|s|s.budget.as_ref()).map(|b|b.limit),
            "accepted":accepted,"accepted_conversation":accepted_conversation.map(|id|format!("{}/{id}",view.client_mode)),"at_bottom":at_bottom,
            "messages":view.main_snapshot.as_ref().map(|s|s.messages.as_slice()).unwrap_or(&[]).iter().map(|m|serde_json::json!({"id":m.id,"role":m.role,"status":m.status})).collect::<Vec<_>>()
        });
        if let Ok(json) = CString::new(state.to_string()) {
            unsafe { pot_bridge_update(json.as_ptr()) };
        }
        changed
    }
}
impl Shell {
    fn render_content(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let view = self.client.view.lock().unwrap().clone();
        let snapshot = view.snapshot.as_ref();
        let running = snapshot.is_some_and(|s| s.turn.is_some());
        let stopping = snapshot
            .and_then(|s| s.turn.as_ref())
            .is_some_and(|t| t.cancelling);
        let messages = if view.selected == "new" {
            vec![]
        } else {
            snapshot.map(|s| s.messages.clone()).unwrap_or_default()
        };
        let at_bottom = self.scroll.max_offset().y + self.scroll.offset().y <= px(32.);
        let dark = unsafe { pot_bridge_dark() };
        let mode = if dark {
            ThemeMode::Dark
        } else {
            ThemeMode::Light
        };
        if Theme::global(cx).mode != mode {
            Theme::change(mode, Some(window), cx);
            design::install(cx);
        }
        cx.set_global(design::Viewport(window.viewport_size().width));
        if self.settings.localize(window, cx) {
            self.task_search.update(cx, |input, cx| {
                input.set_placeholder(
                    crate::i18n::text("main.search-task-title-or-id-47"),
                    window,
                    cx,
                )
            });
            self.usage_ui.localize(self.catalog(), window, cx);
        }
        self.settings.sync(&self.native["settings"], window, cx);
        match self.native["page"].as_u64() {
            Some(4..=6) => {
                let handler = self.page_handler(cx);
                return self.usage_ui.render(
                    pot_ui::usage::UsageView {
                        page: self.native["page"].as_u64().unwrap(),
                        usage: &view.usage,
                        usage_error: &view.usage_error,
                        usage_notice: &view.usage_notice,
                        usage_pending: view.usage_pending,
                        controls: true,
                        selected_title: view.selected_task.as_ref().map(|t| t.title.as_str()),
                    },
                    pot_ui::pages::Layout::NATIVE,
                    self.catalog(),
                    handler,
                    window,
                    cx,
                );
            }
            Some(2) => {
                return self.settings.render(&self.native["settings"], cx).child(
                    Button::new("settings-resource-cache")
                        .ghost()
                        .label(crate::i18n::text("resources.cache"))
                        .on_click(|_, _, _| {
                            ui::command(serde_json::json!({"action":"resource-cache"}))
                        }),
                );
            }
            Some(1) => {
                let catalog = self.catalog();
                let handler = self.page_handler(cx);
                let connections = self.machine_connections.render(
                    pot_ui::machine_connections::View {
                        client: Some(&backend::control_projection()),
                        directory: &view.machines,
                        selected: &self.machine_selection,
                        reply: &view.machine_reply,
                        pending: view.machine_pending,
                    },
                    catalog,
                    handler.clone(),
                    window,
                    cx,
                );
                return div()
                    .h_full()
                    .flex()
                    .flex_col()
                    .child(div().ml(px(56.)).child(connections))
                    .when(!view.error.is_empty(), |el| {
                        el.child(
                            div()
                                .ml(px(56.))
                                .p_3()
                                .child(crate::i18n::message(&view.error)),
                        )
                    })
                    .child(pot_ui::machines::render(
                        pot_ui::machines::MachinesView {
                            permissions: &view.permissions,
                            directory: &view.machines,
                            preferred: &self.machine_selection,
                            desktop_runtime: (backend::mode() == "local")
                                .then(|| view.runtime["runtime_id"].as_str())
                                .flatten(),
                        },
                        pot_ui::pages::Layout::NATIVE,
                        self.catalog(),
                        handler,
                        cx,
                    ));
            }
            _ => {}
        }
        if !self.show_tasks
            && view.selected.is_empty()
            && (view
                .main_snapshot
                .as_ref()
                .is_some_and(|s| s.agent.phase == pot_protocol::AgentPhase::NeedsConfiguration))
        {
            return ui::page(cx).child(
                ui::content().child(
                    ui::card(cx)
                        .child(ui::title(crate::i18n::text("onboarding.start")))
                        .child(ui::hint(
                            crate::i18n::text("onboarding.choose-connection"),
                            cx,
                        ))
                        .child(
                            Button::new("chat-ai-setup")
                                .primary()
                                .label(crate::i18n::text("main.open-ai-configuration"))
                                .on_click(|_, _, _| {
                                    ui::command(serde_json::json!({"action":"ai-open"}))
                                }),
                        )
                        .child(
                            Button::new("chat-control-setup")
                                .ghost()
                                .label(crate::i18n::text("onboarding.connect-server"))
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.machine_connections.open = true;
                                    ui::command(serde_json::json!({"action":"page","value":1}));
                                    cx.notify();
                                })),
                        ),
                ),
            );
        }
        if !view.selected.is_empty()
            && view.selected != "new"
            && (view.error.contains("404") || view.error.contains("task_missing"))
        {
            return ui::page(cx).child(
                ui::content()
                    .child(ui::empty(
                        IconName::Inbox,
                        crate::i18n::text("main.this-task-is-no-longer-available-return"),
                        cx,
                    ))
                    .child(
                        Button::new("missing-task-list")
                            .ghost()
                            .small()
                            .label(crate::i18n::text("main.back-to-tasks"))
                            .on_click(cx.listener(|this, _, _, cx| {
                                this.show_tasks = true;
                                ui::command(serde_json::json!({"action":"page","value":3}));
                                cx.notify();
                            })),
                    ),
            );
        }
        let bg = if dark { 0x202020 } else { 0xfdfdfd };
        let ink = if dark { 0xf2f2f2 } else { 0x262626 };
        let muted = if dark { 0xb7b7b7 } else { 0x646464 };
        let bubble = if dark { 0x2b2b2b } else { 0xffffff };
        let error = if !view.command_error.is_empty() {
            crate::i18n::message(&view.command_error)
        } else {
            crate::i18n::message(&view.error)
        };
        if self.show_tasks && self.schedule_tab {
            return self.schedules_ui.render(&view, cx);
        }
        if self.show_tasks {
            let handler = self.page_handler(cx);
            let navigation = schedules_ui::navigation(false, cx);
            return pot_ui::tasks::render(
                pot_ui::tasks::TasksView {
                    local_runtime: view.runtime["runtime_id"].as_str().unwrap_or_default(),
                    assistant_names: &view.assistants,
                    search: &self.task_search,
                    scroll: &self.tasks_scroll,
                    navigation,
                    tasks: &view.tasks,
                    task_page: &view.task_page,
                    task_query: &view.task_query,
                    tasks_loading: view.tasks_loading,
                    connected: view.main_snapshot.is_some(),
                    error: &view.error,
                    parallel: Some(self.native["settings"]["parallel"].as_str().unwrap_or("5")),
                },
                pot_ui::pages::Layout::NATIVE,
                self.catalog(),
                handler,
                window,
                cx,
            );
        }
        let assistant_handler = self.page_handler(cx);
        let assistant_header = self.assistants_ui.render(
            pot_ui::assistants::View {
                assistants: &view.assistants,
                current: view.assistant_id(),
                rules: &view.assistant_rules,
            },
            self.catalog(),
            assistant_handler,
            window,
            cx,
        );
        let latest_assistant = messages.iter().rposition(|m| m.role == Role::Assistant);
        div()
            .size_full()
            .pl(px(56.))
            .flex()
            .flex_col()
            .bg(rgb(bg))
            .text_color(rgb(ink))
            .text_size(px(15.))
            .font_family(".AppleSystemUIFont")
            .when(view.selected.is_empty(), |el| el.child(assistant_header))
            .when(!view.selected.is_empty(), |el| {
                let task = view
                    .selected_task
                    .as_ref()
                    .filter(|t| t.id == view.selected);
                el.child(
                    div().flex_shrink_0().px_8().py_3().child(
                        div()
                            .w_full()
                            .max_w(px(760.))
                            .mx_auto()
                            .flex()
                            .flex_col()
                            .gap_2()
                            .child(
                                div()
                                    .flex()
                                    .items_center()
                                    .gap_3()
                                    .text_size(px(12.))
                                    .text_color(cx.theme().muted_foreground)
                                    .when_some(task, |el, task| {
                                        el.child(
                                            div()
                                                .flex()
                                                .items_center()
                                                .gap_1()
                                                .child(
                                                    Icon::new(
                                                        if task.phase
                                                            == pot_protocol::TaskPhase::Complete
                                                        {
                                                            IconName::Check
                                                        } else {
                                                            IconName::Info
                                                        },
                                                    )
                                                    .size(px(13.)),
                                                )
                                                .child(if task.remote_waiting { crate::i18n::text("main.waiting-for-remote-machine") } else { task_phase(&task.phase) })
                                                .child(format!(" · {}", pot_ui::machine_state::task_location(task, self.catalog(), view.runtime["runtime_id"].as_str().unwrap_or_default()))),
                                        )
                                        .when(
                                            task.phase.scope() == "archived",
                                            |el| {
                                                el.child(
                                                    div()
                                                        .flex()
                                                        .items_center()
                                                        .gap_1()
                                                        .child(
                                                            Icon::new(IconName::Inbox)
                                                                .size(px(13.)),
                                                        )
                                                        .child(crate::i18n::text("main.archived")),
                                                )
                                            },
                                        )
                                    })
                                    .when_some(task, |el, task| {
                                        let owner = task.id.clone();
                                        let title = task.title.clone();
                                        el.child(Button::new("task-profile").ghost().small().label(crate::i18n::text("main.change-agent"))
                                            .on_click(move |_,_,_|ui::command(serde_json::json!({"action":"task-profile","owner":owner,"title":title}))))
                                    })
                                    .when(
                                        task.is_some_and(|t| {
                                            matches!(
                                                t.phase,
                                                pot_protocol::TaskPhase::Running
                                                    | pot_protocol::TaskPhase::Queued
                                                    | pot_protocol::TaskPhase::Waiting
                                            )
                                        }),
                                        |el| {
                                            el.child(
                                                Button::new("stop-task")
                                                    .ghost()
                                                    .small()
                                                    .label(crate::i18n::text("main.stop"))
                                                    .on_click(cx.listener(|this, _, _, _| {
                                                        this.client.cancel()
                                                    })),
                                            )
                                        },
                                    ),
                            ),
                    ),
                )
            })
            .child(
                div()
                    .id("messages")
                    .flex_1()
                    .min_h_0()
                    .overflow_y_scroll()
                    .track_scroll(&self.scroll)
                    .px_8()
                    .pt_8()
                    .pb_4()
                    .flex()
                    .flex_col()
                    .gap_5()
                    .child(pot_ui::permissions::cards(&view.permissions, &view.machines, if view.selected.is_empty() { view.assistant_id() } else { &view.selected }, self.catalog(), self.page_handler(cx), cx))
                    .child(pot_ui::resources::cards(&self.native["resource_requests"],self.catalog(),self.page_handler(cx),cx))
                    .when(messages.is_empty(), |el| {
                        el.child(
                            div()
                                .mx_auto()
                                .mt_16()
                                .text_color(rgb(muted))
                                .child(crate::i18n::text("main.what-would-you-like-pot-to-do")),
                        )
                    })
                    .children(messages.iter().enumerate().map(|(index, m)| {
                        let latest_reply =
                            !running && index + 1 == messages.len() && m.role == Role::Assistant;
                        let artifact = artifacts::from_message(
                            m,
                            &if view.selected.is_empty() {
                                client::root().join("workspace/workspace")
                            } else {
                                client::root()
                                    .join("workspace/workspace/tasks")
                                    .join(&view.selected)
                            },
                        );
                        let source = pot_ui::chat::display_source(m, crate::i18n::report, crate::i18n::message);
                        let artifact = artifact.map(|path| {
                            let label = crate::i18n::tr!("main.show-in-finder", path.file_name().unwrap_or_default().to_string_lossy());
                            div().id("artifact").mt_2().text_size(px(12.)).cursor_pointer().text_color(rgb(0x079fee)).child(label)
                                .on_click(move |_, _, _| { let _ = std::process::Command::new("open").arg("-R").arg(&path).spawn(); }).into_any_element()
                        });
                        let labels = pot_ui::chat::Labels::new(|key| crate::i18n::text(key).into());
                        let handler: pot_ui::chat::ChatHandler = std::sync::Arc::new(cx.listener(|this, action: &pot_ui::chat::ChatAction, _, cx| {
                            match action {
                                pot_ui::chat::ChatAction::ToggleTool(id) => { if !this.expanded.remove(id) { this.expanded.insert(id.clone()); } }
                                pot_ui::chat::ChatAction::OpenTask(id) => { this.client.select(id.clone()); this.show_tasks=false; this.first=true; }
                                pot_ui::chat::ChatAction::DownloadFile(link) => {
                                    if let Some((task, path)) = pot_protocol::parse_file_link(link) {
                                        let owner = this.client.view.lock().unwrap().assistant_id().to_owned();
                                        ui::command(serde_json::json!({"action":"file-download","task_id":task,"path":path,"owner":owner}));
                                    }
                                }
                            }
                            cx.notify();
                        }));
                        pot_ui::chat::message(pot_ui::chat::MessageView {
                            message: m, source: &source, labels: &labels, artifact,
                            options: pot_ui::chat::MessageOptions { dark, expanded:self.expanded.contains(&m.id), latest_reply, touch:false, available_width:f32::from(window.viewport_size().width) },
                        }, handler, cx)
                            .when(view.selected.is_empty() && pot_ui::onboarding::missing_desktop(m), |el| {
                                el.child(ui::card(cx).mt_2()
                                    .child(crate::i18n::text("onboarding.desktop-failure"))
                                    .child(div().flex().flex_wrap().gap_2()
                                        .child(Button::new(SharedString::from(format!("desktop-create-{}", m.id)))
                                            .ghost().small().label(crate::i18n::text("onboarding.create-desktop"))
                                            .on_click(|_, _, _| ui::command(serde_json::json!({"action":"sandbox-onboarding"}))))
                                        .child(Button::new(SharedString::from(format!("desktop-connect-{}", m.id)))
                                            .ghost().small().label(crate::i18n::text("onboarding.connect-desktop"))
                                            .on_click(cx.listener(|this, _, _, cx| {
                                                this.machine_connections.open = true;
                                                ui::command(serde_json::json!({"action":"page","value":1}));
                                                cx.notify();
                                            }))))
                                )
                            })
                            .when(view.selected.is_empty() && latest_assistant == Some(index) && (!view.running_tasks.is_empty() || self.summary_removal.is_some()), |el| {
                                el.child(div().mt_2().w_full().max_w(px(600.)).flex().flex_col().gap_1().text_size(px(12.)).text_color(rgb(muted))
                                    .children(view.running_tasks.iter().map(|task| {
                                        let id = task.id.clone();
                                        Button::new(SharedString::from(format!("running-{}", task.id)))
                                            .ghost().small().text_size(px(12.)).text_color(rgb(muted))
                                            .accessibility_label(crate::i18n::tr!("main.open-task", task.title, task.progress))
                                            .child(div().w_full().flex().items_center().gap_2().text_size(px(12.)).font_weight(FontWeight::NORMAL)
                                                .child(div().size(px(4.)).rounded_full().bg(rgb(if dark { 0x7d9ec3 } else { 0x6f8ba8 }))
                                                    .with_animation(SharedString::from(format!("activity-{}", task.id)), Animation::new(std::time::Duration::from_millis(2700)).repeat(), |el, t| el.opacity(0.825 + 0.175 * (t * std::f32::consts::TAU).cos())))
                                                .child(ui::brand_icon(&task.executor_icon).size(px(14.)))
                                                .child(div().max_w(px(560.)).overflow_hidden().whitespace_nowrap().child(task.title.clone())))
                                            .on_click(cx.listener(move |this, _, _, cx| { this.client.select(id.clone()); this.show_tasks=false; this.first=true; cx.notify(); }))
                                    }))
                                    .when_some(self.summary_removal, |el, (start, count)| {
                                        el.child(div().id("summary-collapse").h(px(24. * count as f32)).w_full().with_animation(
                                            SharedString::from(format!("summary-collapse-{start:?}")), Animation::new(std::time::Duration::from_millis(210)),
                                            move |el, t| el.h(px(24. * count as f32 * (1. - t))).opacity(1. - t)))
                                    })
                                    .when(view.running_total > 3, |el| el.child(Button::new("running-overflow").ghost().small().text_color(rgb(muted)).accessibility_label(crate::i18n::tr!("main.more-running-view-tasks", view.running_total-3)).child(div().w_full().text_size(px(12.)).font_weight(FontWeight::NORMAL).child(crate::i18n::tr!("main.more-running", view.running_total-3)))
                                        .on_click(cx.listener(|this, _, window, cx| { this.task_search.update(cx, |field, cx| field.set_value("", window, cx)); ui::command(serde_json::json!({"action":"page", "value":3})); this.show_tasks=true; this.schedule_tab=false; this.client.filter_tasks("active".into(), String::new(), 0); cx.notify(); })))))
                            })
                    })),
            )
            .child(
                div().w_full().px_8().flex_shrink_0().child(
                    div()
                        .w_full()
                        .max_w(px(760.))
                        .mx_auto()
                        .text_size(px(12.))
                        .text_color(rgb(muted))
                        .when(!error.is_empty(), |el| el.child(div().py_2().child(error)))
                        .when_some(self.native["sandbox_error"].as_str().filter(|error| !error.is_empty()), |el, error| {
                            el.child(div().py_2().child(error.to_owned()))
                        })
                        .when(running, |el| {
                            el.child(div().py_2().child(if stopping {
                                crate::i18n::text("main.stopping")
                            } else {
                                crate::i18n::text("main.pot-is-working")
                            }))
                        })
                        .when(
                            snapshot
                                .and_then(|s| s.budget.as_ref())
                                .is_some_and(|b| b.remaining() == 0),
                            |el| el.child(crate::i18n::text("main.request-allowance-exhausted-adjust-it-in-settings")),
                        )
                        .when(!at_bottom && !self.jumping_to_latest, |el| {
                            el.child(
                                Button::new("latest")
                                    .ghost()
                                    .w_full()
                                    .h(px(32.))
                                    .bg(rgb(bubble))
                                    .cursor_pointer()
                                    .label(crate::i18n::text("main.latest-messages"))
                                    .accessibility_label(crate::i18n::text("main.scroll-to-latest-messages"))
                                    .on_click(cx.listener(|this, _, _, cx| {
                                        this.jumping_to_latest = true;
                                        this.scroll.scroll_to_bottom();
                                        cx.notify();
                                    })),
                            )
                        }),
                ),
            )
            .child(
                div()
                    .flex_shrink_0()
                    // Native composer: 24 px bottom inset. Remove its extra
                    // 24 px top clearance while the latest-message bar is shown.
                    .h(px(unsafe { pot_bridge_composer_height() } as f32
                        - if !at_bottom && !self.jumping_to_latest {
                            24.
                        } else {
                            0.
                        })),
            )
    }
}
impl Render for Shell {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        if std::mem::take(&mut self.pending_mode) {
            self.switch_mode(window, cx);
        }
        if std::mem::take(&mut self.pending_up) {
            self.navigate_up(window, cx);
        }
        let restoring_view = self.client.view.lock().unwrap().clone();
        if !restoring_view.tasks_loading
            && !restoring_view.usage_pending
            && !restoring_view.schedule_pending
            && let Some(mut offsets) = self.pending_offsets.take()
        {
            if let Some((id, inset)) = self.pending_task_anchor.take()
                && let Some(index) = restoring_view.tasks.iter().position(|t| t.id == id)
            {
                offsets[1].y = px(-(index as f32 * 80. + inset));
            }
            self.scroll.set_offset(offsets[0]);
            self.tasks_scroll.set_offset(offsets[1]);
            self.settings.scroll.set_offset(offsets[2]);
            self.schedules_ui.scroll.set_offset(offsets[3]);
            self.usage_ui.scroll.set_offset(offsets[4]);
        }
        let body = self.render_content(window, cx);
        self.observe_navigation();
        let view = self.client.view.lock().unwrap().clone();
        let body = div().flex_1().min_h_0().child(body);
        let body = if cx.reduce_motion() {
            body.into_any_element()
        } else {
            body.with_animation(
                SharedString::from(format!("route-enter-{}", self.navigation.generation)),
                Animation::new(std::time::Duration::from_millis(180)),
                |el, t| el.opacity(0.94 + 0.06 * t),
            )
            .into_any_element()
        };
        div()
            .size_full()
            .flex()
            .flex_col()
            .bg(cx.theme().background)
            .child(self.titlebar(&view, window, cx))
            .child(body)
    }
}

fn task_phase(p: &pot_protocol::TaskPhase) -> &'static str {
    use pot_protocol::TaskPhase::*;
    match p {
        Queued => crate::i18n::text("schedules-ui.queued"),
        Waiting => crate::i18n::text("main.waiting-for-allowance"),
        Running => crate::i18n::text("schedules-ui.running"),
        Complete => crate::i18n::text("schedules-ui.completed"),
        Failed => crate::i18n::text("main.failed-add-instructions-to-continue"),
        Cancelled => crate::i18n::text("main.stopped"),
        Interrupted => crate::i18n::text("main.interrupted-add-instructions-to-resume"),
    }
}
fn startup_error(error: &str) -> ! {
    eprintln!("Cannot start Pot: {error}");
    if let Ok(message) = CString::new(error) {
        unsafe {
            pot_bridge_startup_error(message.as_ptr());
        }
    }
    std::process::exit(1)
}
fn main() {
    // Configure the embedded backend before GPUI or Tokio starts any threads.
    let root = client::root();
    if let Err(error) = unsafe { pot_server::apply_settings(&root) } {
        startup_error(&error.to_string());
    }
    if let Some(resources) = std::env::current_exe()
        .ok()
        .and_then(|p| {
            p.parent()
                .and_then(|p| p.parent())
                .map(|p| p.join("Resources/runtime"))
        })
        .filter(|p| p.is_dir())
    {
        unsafe {
            std::env::set_var("POT_RUNTIME_RESOURCES", &resources);
            std::env::set_var("POT_BUILTIN_SKILLS_DIR", resources.join("skills"));
            std::env::set_var("POT_MACOS_HELPER", resources.join("PotMacOSHelper"));
            std::env::set_var(
                "POT_CONTAINER_HELPER",
                resources
                    .parent()
                    .unwrap()
                    .parent()
                    .unwrap()
                    .join("Helpers/PotHelper"),
            );
            if std::env::var_os("POT_ACCESS_ASSETS").is_none() {
                std::env::set_var("POT_ACCESS_ASSETS", resources.join("mobile"));
            }
            std::env::set_var(
                "PATH",
                format!(
                    "{}:{}",
                    resources.join("node/bin").display(),
                    std::env::var("PATH").unwrap_or_default()
                ),
            );
        }
    }

    if let Err(error) = backend::ready() {
        startup_error(&error);
    }
    gpui_kit::application()
        .with_assets(gpui_kit::assets::Assets)
        .run(|cx: &mut App| {
            gpui_kit::init(cx);
            crate::i18n::set_locale(if unsafe { pot_bridge_chinese() } {
                "zh-CN"
            } else {
                "en"
            });
            cx.set_global(design::Viewport(px(1080.)));
            design::install(cx);
            gpui_kit::open_window(
                WindowOptions {
                    window_bounds: Some(WindowBounds::Windowed(Bounds::centered(
                        None,
                        size(px(1080.), px(760.)),
                        cx,
                    ))),
                    window_min_size: Some(size(px(800.), px(700.))),
                    titlebar: Some(TitlebarOptions {
                        traffic_light_position: Some(point(px(12.), px(15.))),
                        ..gpui_kit::component::TitleBar::title_bar_options()
                    }),
                    ..gpui_kit::component::TitleBar::window_options()
                },
                cx,
                |window, cx| {
                    window.set_window_title("Pot");
                    window.on_window_should_close(cx, |_, _| {
                        unsafe { pot_bridge_hide() };
                        false
                    });
                    cx.new(|cx: &mut Context<Shell>| {
                        let (sender, receiver) = async_channel::bounded(1);
                        let _ = UI_WAKE.set(sender.clone());
                        unsafe {
                            pot_bridge_set_wake(wake_ui);
                        }
                        // Runtime snapshots and native geometry still use a bounded poll;
                        // native commands bypass it and wake this task immediately.
                        cx.background_spawn(async move {
                            loop {
                                smol::Timer::after(std::time::Duration::from_millis(200)).await;
                                if sender.is_closed() {
                                    break;
                                }
                                let _ = sender.try_send(());
                            }
                        })
                        .detach();
                        cx.spawn(async move |this, cx| {
                            while receiver.recv().await.is_ok() {
                                if this
                                    .update(cx, |this, cx| {
                                        if this.tick(cx) {
                                            cx.notify();
                                        }
                                    })
                                    .is_err()
                                {
                                    break;
                                }
                            }
                        })
                        .detach();
                        Shell {
                            other_mode: None,
                            pending_mode: false,
                            client: client::Client::start(),
                            assistants_ui: Default::default(),
                            machine_connections: Default::default(),
                            machine_selection: serde_json::Value::Null,
                            scroll: ScrollHandle::new(),
                            tasks_scroll: ScrollHandle::new(),
                            navigation: navigation::Navigation::default(),
                            pending_up: false,
                            pending_offsets: None,
                            pending_task_anchor: None,
                            last_content: String::new(),
                            summary_count: 0,
                            summary_removal: None,
                            expanded: HashSet::new(),
                            first: true,
                            show_tasks: false,
                            last_view: None,
                            last_dark: false,
                            last_height: 0.,
                            last_at_bottom: true,
                            jumping_to_latest: false,
                            native: serde_json::Value::Null,
                            usage_ui: usage_ui::UsageUI::new(
                                pot_ui::localization::Catalog::new(&rust_i18n::locale()),
                                window,
                                cx,
                            ),
                            settings: ui::Settings::new(window, cx),
                            schedule_tab: false,
                            schedules_ui: schedules_ui::SchedulesUI::new(window, cx),
                            task_search: cx.new(|cx| {
                                InputState::new(window, cx).placeholder(crate::i18n::text(
                                    "main.search-task-title-or-id-47",
                                ))
                            }),
                        }
                    })
                },
            )
            .unwrap();
            let html = CString::new("<html></html>").unwrap();
            unsafe { pot_bridge_install(html.as_ptr()) };
            cx.activate(true);
        });
}
