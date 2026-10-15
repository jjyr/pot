//! One application presentation tree for native and browser hosts.
mod assets;
mod composer;
mod navigation;
mod schedules_ui;
pub mod settings_state;
mod spaces_ui;
mod ui;
mod usage_ui;
pub use assets::Assets;
pub mod client;
pub mod i18n;
pub mod platform;
pub mod spaces;
use crate as pot_ui;
use crate::design;
use crate::gpui;
use gpui::{prelude::*, *};
use gpui_kit::component::{
    ActiveTheme, Icon, IconName, Sizable, Theme, ThemeMode,
    button::{Button, ButtonVariants},
    input::InputState,
};
use platform as backend;
use pot_protocol::Role;
use std::collections::HashSet;
struct ModeCache {
    settings_state: settings_state::SettingsState,
    pub client: client::Client,
    navigation: navigation::Navigation<navigation::State>,
    assistants_ui: pot_ui::assistants::Editor,
    machine_connections: pot_ui::machine_connections::Editor,
    settings: ui::Settings,
    schedules_ui: schedules_ui::SchedulesUI,
    usage_ui: usage_ui::UsageUI,
    machine_selection: serde_json::Value,
}
pub struct Shell {
    page: u64,
    pending_search: Option<String>,
    configuration_assistant: Option<String>,
    brand: [std::sync::Arc<Image>; 2],
    settings_state: settings_state::SettingsState,
    composer: composer::Composer,
    other_mode: Option<ModeCache>,
    assistant_list: bool,
    space_directory: spaces::Directory,
    usage_location: String,
    settings_return: Option<(spaces::Owner, String)>,
    notifications: Vec<(spaces::Owner, pot_protocol::TaskInfo)>,
    pub client: client::Client,
    scroll: ScrollHandle,
    tasks_scroll: ScrollHandle,
    navigation: navigation::Navigation<navigation::State>,
    pending_up: bool,
    pending_offsets: Option<[Point<Pixels>; 5]>,
    pending_task_anchor: Option<(String, f32)>,
    last_content: String,
    bridge_projection: serde_json::Value,
    route_projection: serde_json::Value,
    summary_count: usize,
    summary_removal: Option<(web_time::Instant, usize)>,
    expanded: HashSet<String>,
    first: bool,
    show_tasks: bool,
    last_view: Option<client::View>,
    last_dark: bool,
    last_height: f64,
    last_at_bottom: bool,
    jumping_to_latest: bool,
    host: serde_json::Value,
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
            self.machine_connections.connect();
            ui::command(serde_json::json!({"action":"page","value":1}));
            return;
        }
        let mut other = self.other_mode.take().unwrap_or_else(|| ModeCache {
            settings_state: Default::default(),
            client: client::Client::start_mode(&backend::mode()),
            navigation: Default::default(),
            assistants_ui: Default::default(),
            machine_connections: Default::default(),
            settings: ui::Settings::new(window, cx),
            schedules_ui: schedules_ui::SchedulesUI::new(window, cx),
            usage_ui: usage_ui::UsageUI::new(self.catalog(), window, cx),
            machine_selection: serde_json::Value::Null,
        });
        std::mem::swap(&mut self.settings_state, &mut other.settings_state);
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
        self.configuration_assistant = None;
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
        pot_ui::localization::Catalog::new(&i18n::locale())
    }
    fn page_handler(&self, cx: &Context<Self>) -> pot_ui::pages::Handler {
        std::rc::Rc::new(
            cx.listener(|this, event: &pot_ui::pages::PageAction, window, cx| {
                let Some(event) = this.usage_ui.apply(event, this.catalog(), cx) else {
                    cx.notify();
                    return;
                };
                match event.kind {
                    "assistant-edit" => { this.assistants_ui.form_only = true; this.assistants_ui.select(event.value.as_str().unwrap_or("main")); },
                    "assistant-new" => { this.assistants_ui.form_only = true; this.assistants_ui.select("new"); },
                    "assistant-select" => { this.client.select_assistant(event.value.as_str().unwrap_or("main").into()); this.assistants_ui.select(event.value.as_str().unwrap_or("main")); },
                    "assistant-reload" => this.assistants_ui.reload(event.value.as_str().unwrap_or("main")),
                    "assistant-close" => this.assistants_ui.open = false,
                    "machine-connections-toggle" => this.machine_connections.open = !this.machine_connections.open,
                    "machine-command" => this.client.machine_command(event.value),
                    "assistant-save" => this.client.assistant_save(event.value),
                    "assistant-model" => ui::command(serde_json::json!({"action":"ai-open","owner":event.value,"return_to_chat":true})),
                    "permission-decision" => this.client.permission_decision(event.value),
                    "sandbox-lifecycle" => this.client.sandbox_lifecycle(event.value),
                    "native" => ui::command(event.value),
                    "machine-select" => {
                        this.machine_selection = event.value.clone();
                        ui::command(serde_json::json!({"action":"machines-detail","value":true}));
                    }
                    "execution-target-tasks" => {
                        this.client.filter_execution_target(event.value.to_string());
                        this.show_tasks = true;
                        ui::command(serde_json::json!({"action":"page","value":3}));
                        this.client.refresh();
                    }
                    "execution-target-filter-clear" => {
                        this.client.filter_execution_target(String::new());
                        this.client.refresh();
                    }
                    "execution-target-desktop" => {
                        ui::command(serde_json::json!({"action":"execution-target-desktop","id":event.value["target"]["sandbox_id"]}));
                    }
                    "sandbox-select" => {
                        ui::command(serde_json::json!({"action":"sandbox-view","id":event.value}))
                    }
                    "refresh" => this.client.refresh(),
                    "task-filter" | "task-clear-search" | "task-previous" | "task-next" => {
                        let mut v = this.client.view.lock().unwrap().clone();
                        if platform::with(|p|p.aggregates_spaces()) {v.task_page = this.space_directory.task_page(&v.task_query);}
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
                        if let Some((owner, id)) = this.space_directory.task_owner(event.value["id"].as_str().unwrap_or("")) {
                            if this.activate_owner(&owner, window, cx) { this.client.select(id); this.show_tasks = false; this.assistant_list = false; ui::command(serde_json::json!({"action":"page","value":3})); }
                            cx.notify(); return;
                        }
                        this.show_tasks = false;
                        if let Some(owner)=event.value["assistant_id"].as_str(){this.client.select_assistant(owner.into());}
                        this.client
                            .select(event.value["id"].as_str().or(event.value.as_str()).unwrap_or("").into());
                        this.first = true;
                    }
                    "usage-page" => {
                        if event.value == 6 && this.host["page"] == 4 {
                            if this.usage_location.is_empty() { return; }
                            let Some(owner)=this.space_directory.spaces.get(&this.usage_location).map(|s|s.owner("main")) else{return;};
                            this.settings_return=this.capture_chat();
                            if !this.activate_owner(&owner,window,cx){return;}
                        }
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

    pub fn tick(&mut self, cx: &mut Context<Self>) -> bool {
        for (mode, domains) in spaces::take_native_changes() {
            if domains.contains("settings") && mode == self.client.view.lock().unwrap().client_mode
            {
                self.settings_state.invalidate();
            }
            ui::command(
                serde_json::json!({"action":"backend-changed","mode":mode,"domains":domains}),
            );
        }
        let directory = spaces::directory().lock().unwrap().clone();
        let directory_changed = directory != self.space_directory;
        for space in directory.spaces.values() {
            if let Some(previous) = self
                .space_directory
                .spaces
                .get(&space.mode)
                .filter(|s| s.id == space.id)
            {
                for task in &space.tasks {
                    if matches!(
                        task.phase,
                        pot_protocol::TaskPhase::Complete
                            | pot_protocol::TaskPhase::Failed
                            | pot_protocol::TaskPhase::Interrupted
                    ) && previous.tasks.iter().any(|t| {
                        t.id == task.id
                            && t.assistant_id == task.assistant_id
                            && t.phase != task.phase
                    }) {
                        self.notifications
                            .push((space.owner(&task.assistant_id), task.clone()));
                    }
                }
            }
        }
        if self.notifications.len() > 20 {
            self.notifications.drain(..self.notifications.len() - 20);
        }
        self.space_directory = directory;
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
        let created = self.client.view.lock().unwrap().created_assistant.take();
        if let Some(owner) = created {
            self.client.select_assistant(owner.clone());
            self.client.select(String::new());
            self.assistant_list = false;
            self.show_tasks = false;
            self.assistants_ui.form_only = true;
            self.assistants_ui.select(&owner);
            ui::command(serde_json::json!({"action":"page","value":0}));
        }
        let old_tasks = self.show_tasks;
        {
            let mut v = self.client.view.lock().unwrap();
            let visible = self.show_tasks && self.schedule_tab;
            let changed = v.schedules_visible != visible;
            v.schedules_visible = visible;
            drop(v);
            if changed {
                self.client.refresh();
            }
        }
        let success = self.client.view.lock().unwrap().schedule_success;
        if success != self.schedules_ui.success {
            self.schedules_ui.success = success;
            self.schedules_ui.open = false;
        }
        {
            let view = self.client.view.lock().unwrap();
            self.settings_state.bind(
                &view.client_mode,
                view.runtime["runtime_id"].as_str().unwrap_or_default(),
                self.configuration_assistant
                    .as_deref()
                    .unwrap_or_else(|| view.assistant_id()),
            );
        }
        if self.client.view.lock().unwrap().snapshot.is_some() {
            for request in self.settings_state.ensure_loaded() {
                platform::with(|p| p.request(request));
            }
        }
        let events = platform::with(|p| p.events());
        let intents_changed = !events.is_empty();
        for event in events {
            match event["action"].as_str() {
                Some("navigation-command") => {
                    self.project_page(event["value"]["value"].as_u64().unwrap_or(0))
                }
                Some("navigation") => {
                    let page = event["value"].as_u64().unwrap_or(0);
                    if page == 0 {
                        self.assistant_list = true;
                        self.client.select(String::new());
                    }
                    self.show_tasks = page == 3;
                    self.schedule_tab = false;
                    self.project_page(page);
                }
                Some("task-query-projection") => {
                    self.pending_search = event["q"].as_str().map(str::to_owned)
                }
                Some("settings-changed") => self.settings_state.invalidate(),
                Some("configuration-command") => {
                    let value = &event["value"];
                    if value["action"] == "ai-open"
                        && let Some(owner) = value["owner"].as_str()
                    {
                        self.configuration_assistant = Some(owner.into());
                        let view = self.client.view.lock().unwrap();
                        self.settings_state.bind(
                            &view.client_mode,
                            view.runtime["runtime_id"].as_str().unwrap_or_default(),
                            owner,
                        );
                    }

                    if matches!(value["action"].as_str(), Some("ai-open" | "task-profile")) {
                        self.project_page(2);
                    }
                    for request in self.settings_state.command(value) {
                        platform::with(|p| p.request(request));
                    }
                }
                Some("configuration-reply") => {
                    for request in self.settings_state.reply(
                        event["id"].as_u64().unwrap_or(0),
                        event["context"].as_str().unwrap_or(""),
                        event["body"].clone(),
                        event["error"].as_str(),
                        event["rejected"] == true,
                    ) {
                        platform::with(|p| p.request(request));
                    }
                    if let Some(other) = self.other_mode.as_mut() {
                        for request in other.settings_state.reply(
                            event["id"].as_u64().unwrap_or(0),
                            event["context"].as_str().unwrap_or(""),
                            event["body"].clone(),
                            event["error"].as_str(),
                            event["rejected"] == true,
                        ) {
                            platform::with(|p| p.request(request));
                        }
                    }
                }
                Some("route") => {
                    let page = event["page"].as_str().unwrap_or("chat");
                    self.page = match page {
                        "machines" => 1,
                        "settings" => 2,
                        "tasks" | "schedules" | "task" => 3,
                        "usage" => 4,
                        "conversation-usage" => 5,
                        "quota" => 6,
                        _ => 0,
                    };
                    self.show_tasks = matches!(page, "tasks" | "schedules");
                    self.schedule_tab = page == "schedules";
                    self.assistant_list = page == "assistants";
                }
                Some("composer-key") => self
                    .composer
                    .key(event["key"].as_str().unwrap_or("").into()),
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
                    self.assistant_list = true;
                    self.show_tasks = false;
                    self.client.select(String::new());
                    self.first = true;
                }
                _ => {}
            }
        }
        if let Some(page) = self.settings_state.state["navigate_page"].as_u64() {
            self.settings_state.state["navigate_page"] = serde_json::Value::Null;
            self.assistant_list = false;
            self.project_page(page);
        }
        if self.pending_search.is_none() {
            let search = self.task_search.read(cx).value().to_string();
            let query = self.client.view.lock().unwrap().task_query.clone();
            if search != query.q {
                self.client.filter_tasks(query.scope, search, 0);
            }
        }
        let mut native = platform::with(|p| p.state());
        native["page"] = serde_json::json!(self.page);
        if native["page"] != 2 {
            self.configuration_assistant = None;
        }

        native["settings"] = self.settings_state.projection(
            &native["settings"],
            platform::with(|p| p.capabilities().local_execution_settings),
        );
        let native_changed = native != self.host;
        let language_changed = native["settings"]["locale"]
            .as_str()
            .is_some_and(crate::application::i18n::set_locale);
        self.host = native;
        platform::with(|p| p.watch_machines(self.host["page"] == 1));
        if language_changed {
            cx.notify();
        }
        {
            let mut v = self.client.view.lock().unwrap();
            let page = self.host["page"].as_u64().unwrap_or(0);
            let scope = if page == 4 {
                String::new()
            } else if v.selected.is_empty() {
                v.assistant_id().to_owned()
            } else {
                v.selected.clone()
            };
            let timezone = self.host["timezone"]
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
            let changed = v.usage_query != query;
            if changed {
                v.usage = serde_json::Value::Null;
                v.usage_error.clear();
            }
            v.usage_query = query;
            drop(v);
            if changed {
                self.client.refresh();
            }
        }
        let (accepted, accepted_conversation) = {
            let mut view = self.client.view.lock().unwrap();
            (view.accepted_text.take(), view.accepted_conversation.take())
        };
        if let (Some(text), Some(conversation)) = (&accepted, &accepted_conversation) {
            self.composer.accept(text.clone(), conversation.clone());
        }
        let view = self.client.view.lock().unwrap().clone();
        self.composer.native_sync(&view);
        if !self.composer_visible(&view) {
            self.composer.discard_keys();
        }
        let dark = platform::with(|p| p.dark());
        cx.set_reduce_motion(platform::with(|p| p.reduce_motion()));
        let height = f64::from(self.composer.height + 48.);
        let summary_count = view.running_tasks.len();
        let summary_changed = summary_count != self.summary_count;
        if summary_count < self.summary_count && !cx.reduce_motion() {
            self.summary_removal =
                Some((web_time::Instant::now(), self.summary_count - summary_count));
        }
        self.summary_count = summary_count;
        let removal_finished = self
            .summary_removal
            .is_some_and(|(start, _)| start.elapsed().as_millis() >= 220);
        if removal_finished || cx.reduce_motion() {
            self.summary_removal = None;
        }
        let mut changed = intents_changed
            || directory_changed
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
            "page":self.page,"completions": if view.selected.is_empty() { view.completions.clone() } else { serde_json::Value::Null },
            "assistant_list":self.assistant_list,"can_back":(!self.assistant_list && !self.show_tasks && view.selected.is_empty()) || self.route(&view).parent().is_some(),"boot_id":snapshot.map(|s|s.boot_id.as_str()),"has_snapshot":snapshot.is_some(),"running":view.is_cooking(),"selected_running":running,"selected":view.selected,"assistant_id":view.assistant_id(),"client_mode":view.client_mode,"conversation":format!("{}/{}",view.client_mode,if view.selected.is_empty() {view.assistant_id()} else {&view.selected}),"task_list":self.show_tasks,"can_send":view.can_send(),"connected":view.error.is_empty() && snapshot.is_some(),
            "needs_configuration":snapshot.is_some_and(|s|s.agent.phase == pot_protocol::AgentPhase::NeedsConfiguration),
            "error":snapshot.is_some_and(|s|s.agent.phase == pot_protocol::AgentPhase::Failed),
            "model":snapshot.map(|s|s.agent.model.as_str()).unwrap_or(crate::application::i18n::text("main.connecting")),
            "used":snapshot.and_then(|s|s.budget.as_ref()).map(|b|b.used),
            "limit":snapshot.and_then(|s|s.budget.as_ref()).map(|b|b.limit),
            "accepted":accepted,"accepted_conversation":accepted_conversation.map(|id|format!("{}/{id}",view.client_mode)),"at_bottom":at_bottom,
            "messages":view.main_snapshot.as_ref().map(|s|s.messages.as_slice()).unwrap_or(&[]).iter().map(|m|serde_json::json!({"id":m.id,"role":m.role,"status":m.status})).collect::<Vec<_>>()
        });
        if state != self.bridge_projection || native_changed {
            platform::with(|p| p.project(&state));
            self.bridge_projection = state;
        }
        changed
    }
}
impl Shell {
    fn render_content(&mut self, window: &mut Window, cx: &mut Context<Self>) -> Div {
        let mut view = self.client.view.lock().unwrap().clone();
        if self.host["page"] == 4 {
            let query = (
                self.usage_ui.days,
                self.host["timezone"]
                    .as_str()
                    .unwrap_or("Asia/Shanghai")
                    .to_owned(),
            );
            let directory = spaces::directory();
            let mut directory = directory.lock().unwrap();
            if directory.usage_query.as_ref() != Some(&query) {
                directory.usage_query = Some(query.clone());
                spaces::refresh_directory();
            }
            drop(directory);
            if platform::with(|p| p.aggregates_spaces()) {
                view.usage = self.space_directory.usage(
                    &self.usage_location,
                    &query,
                    self.usage_ui.group_offset,
                );
            }
            view.usage_error = self
                .space_directory
                .spaces
                .values()
                .filter(|s| self.usage_location.is_empty() || self.usage_location == s.mode)
                .filter_map(|s| {
                    if !s.error.is_empty() {
                        Some(format!("{}: {}", s.label(), s.error))
                    } else {
                        None
                    }
                })
                .collect::<Vec<_>>()
                .join("\n");
        }
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
        let dark = platform::with(|p| p.dark());
        let mode = if dark {
            ThemeMode::Dark
        } else {
            ThemeMode::Light
        };
        if Theme::global(cx).mode != mode {
            Theme::change(mode, Some(window), cx);
            design::install(cx);
        }
        cx.set_global(design::Viewport(window.fully_visible_bounds().size.width));
        if self.settings.localize(window, cx) {
            self.task_search.update(cx, |input, cx| {
                input.set_placeholder(
                    crate::application::i18n::text("main.search-task-title-or-id-47"),
                    window,
                    cx,
                )
            });
            self.usage_ui.localize(self.catalog(), window, cx);
        }
        self.settings.sync(&self.host["settings"], window, cx);
        match self.host["page"].as_u64() {
            Some(4..=6) => {
                let handler = self.page_handler(cx);
                let content = self.usage_ui.render(
                    pot_ui::usage::UsageView {
                        page: self.host["page"].as_u64().unwrap(),
                        usage: &view.usage,
                        usage_error: &view.usage_error,
                        usage_notice: &view.usage_notice,
                        usage_pending: view.usage_pending,
                        loading: view.usage.is_null()
                            || (self.host["page"] == 4
                                && self
                                    .space_directory
                                    .spaces
                                    .values()
                                    .filter(|s| {
                                        self.usage_location.is_empty()
                                            || self.usage_location == s.mode
                                    })
                                    .any(|s| {
                                        s.usage_query.as_ref()
                                            != Some(&(
                                                self.usage_ui.days,
                                                self.host["timezone"]
                                                    .as_str()
                                                    .unwrap_or("Asia/Shanghai")
                                                    .to_owned(),
                                            ))
                                    })),
                        controls: self.host["page"] != 4
                            || (!self.usage_location.is_empty()
                                && self
                                    .space_directory
                                    .spaces
                                    .get(&self.usage_location)
                                    .is_some_and(|s| s.connected && s.error.is_empty())),
                        selected_title: view.selected_task.as_ref().map(|t| t.title.as_str()),
                    },
                    pot_ui::pages::Layout {
                        compact: design::compact(cx),
                    },
                    self.catalog(),
                    handler,
                    window,
                    cx,
                );
                return if self.host["page"] == 4 {
                    div()
                        .h_full()
                        .flex()
                        .flex_col()
                        .child(self.location_bar("usage", cx))
                        .child(content.flex_1().min_h_0())
                } else {
                    content
                };
            }
            Some(2) => {
                let location = (self.host["settings"]["section"] != 0)
                    .then(|| self.location_bar("settings", cx));
                return self.settings.render(&self.host["settings"], location, cx);
            }
            Some(1) => return self.render_spaces_machines(window, cx),
            _ => {}
        }
        if !self.show_tasks && view.selected.is_empty() && self.assistant_list {
            return self.render_assistant_list(window, cx);
        }
        if !self.show_tasks && view.selected.is_empty() && self.assistants_ui.open {
            self.assistants_ui.form_only = true;
            let handler = self.page_handler(cx);
            let form = self.assistants_ui.render(
                pot_ui::assistants::View {
                    assistants: &view.assistants,
                    current: view.assistant_id(),
                    rules: &view.assistant_rules,
                },
                self.catalog(),
                handler,
                window,
                cx,
            );
            return ui::page(cx).child(
                ui::content(cx)
                    .child(
                        div()
                            .id("assistant-edit-scroll")
                            .overflow_y_scroll()
                            .child(form),
                    )
                    .when(!view.command_error.is_empty(), |el| {
                        el.child(ui::notice(view.command_error.clone(), true, cx))
                    }),
            );
        }
        if !self.show_tasks
            && view.selected.is_empty()
            && (view
                .main_snapshot
                .as_ref()
                .is_some_and(|s| s.agent.phase == pot_protocol::AgentPhase::NeedsConfiguration))
        {
            return ui::page(cx).child(
                ui::content(cx).child(
                    ui::card(cx)
                        .max_w(px(520.))
                        .mx_auto()
                        .mt_8()
                        .child(ui::title(crate::application::i18n::text(
                            "onboarding.start",
                        )))
                        .child(ui::hint(
                            crate::application::i18n::text("onboarding.choose-connection"),
                            cx,
                        ))
                        .child(
                            Button::new("chat-ai-setup")
                                .primary()
                                .self_start()
                                .label(crate::application::i18n::text("main.open-ai-configuration"))
                                .on_click(|_, _, _| {
                                    ui::command(serde_json::json!({"action":"ai-open"}))
                                }),
                        )
                        .child(
                            Button::new("chat-control-setup")
                                .ghost()
                                .self_start()
                                .label(crate::application::i18n::text("onboarding.connect-server"))
                                .on_click(cx.listener(|this, _, _, cx| {
                                    this.machine_connections.connect();
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
                ui::content(cx)
                    .child(ui::empty(
                        IconName::Inbox,
                        crate::application::i18n::text(
                            "main.this-task-is-no-longer-available-return",
                        ),
                        cx,
                    ))
                    .child(
                        Button::new("missing-task-list")
                            .ghost()
                            .small()
                            .label(crate::application::i18n::text("main.back-to-tasks"))
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
            crate::application::i18n::message(&view.command_error)
        } else {
            crate::application::i18n::message(&view.error)
        };
        if self.show_tasks && self.schedule_tab {
            if view.schedule_selected.is_empty() && !self.schedules_ui.open {
                return self.render_space_schedules(cx);
            }
            return self.schedules_ui.render(&view, cx);
        }
        if self.show_tasks {
            if platform::with(|p| p.aggregates_spaces()) {
                view.task_page = self.space_directory.task_page(&view.task_query);
            }
            view.task_query.offset = view.task_page.offset;
            view.tasks = view.task_page.items.clone();
            if platform::with(|p| p.aggregates_spaces()) {
                view.assistants = self.space_directory.assistants();
                view.error = self.space_directory.error();
                view.tasks_loading = self.space_directory.spaces.is_empty();
            }
            let handler = self.page_handler(cx);
            let navigation = schedules_ui::navigation(false, cx);
            return pot_ui::tasks::render(
                pot_ui::tasks::TasksView {
                    locations: &self.space_directory.task_locations(),
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
                    parallel: platform::with(|p| p.capabilities().local_execution_settings)
                        .then(|| self.host["settings"]["parallel"].as_str().unwrap_or("8")),
                },
                pot_ui::pages::Layout {
                    compact: design::compact(cx),
                },
                self.catalog(),
                handler,
                window,
                cx,
            );
        }
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
                            .when_some(self.space_directory.spaces.get(&backend::mode()), |el, space| {
                                el.child(div().flex().items_center().gap_2()
                                    .child(space.assistant_name(view.assistant_id()).to_owned())
                                    .child(spaces_ui::badge(space, "task-owner")))
                            })
                            .child(
                                div()
                                    .flex()
                                    .flex_wrap()
                                    .items_center()
                                    .gap_3()
                                    .text_size(px(12.))
                                    .text_color(cx.theme().muted_foreground)
                                    .when_some(task, |el, task| {
                                        el.child(
                                            div()
                                                .flex()
                                                .flex_wrap()
                                                .min_w_0()
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
                                                .child(if task.remote_waiting { crate::application::i18n::text("main.waiting-for-remote-machine") } else { task_phase(&task.phase) })
                                                .child(div().min_w_0().max_w(px((f32::from(window.fully_visible_bounds().size.width)-200.).clamp(100.,600.))).truncate().child(format!(" · {}", self.space_directory.spaces.get(&backend::mode()).and_then(|s| self.space_directory.task_locations()[s.owner(view.assistant_id()).object(&task.id)].as_str().map(str::to_owned)).unwrap_or_else(|| pot_ui::machine_state::task_location(task, self.catalog(), view.runtime["runtime_id"].as_str().unwrap_or_default()))))),
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
                                                        .child(crate::application::i18n::text("main.archived")),
                                                )
                                            },
                                        )
                                    })
                                    .when_some(task, |el, task| {
                                        let owner = task.id.clone();
                                        let title = task.title.clone();
                                        el.child(Button::new("task-profile").ghost().small().label(crate::application::i18n::text("main.change-agent"))
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
                                                    .label(crate::application::i18n::text("main.stop"))
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
                    .child(pot_ui::resources::cards(&self.host["resource_requests"],self.catalog(),self.page_handler(cx),cx))
                    .when(messages.is_empty(), |el| {
                        el.child(
                            div()
                                .mx_auto()
                                .mt_16()
                                .text_color(rgb(muted))
                                .child(crate::application::i18n::text("main.what-would-you-like-pot-to-do")),
                        )
                    })
                    .children(messages.iter().enumerate().map(|(index, m)| {
                        let latest_reply =
                            !running && index + 1 == messages.len() && m.role == Role::Assistant;
                        let source = pot_ui::chat::display_source(m, i18n::report, i18n::message);
                        let artifact = platform::with(|p|p.artifact(m, &view.selected)).map(|(name, value)| {
                            let label = i18n::tr!("main.show-in-finder", name);
                            div().id("artifact").mt_2().text_size(px(12.)).cursor_pointer().text_color(rgb(0x079fee)).child(label)
                                .on_click(move |_, _, _| ui::command(value.clone())).into_any_element()
                        });
                        let labels = pot_ui::chat::Labels::new(|key| crate::application::i18n::text(key).into());
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
                            options: pot_ui::chat::MessageOptions { dark, expanded:self.expanded.contains(&m.id), latest_reply, touch:false, available_width:f32::from(window.fully_visible_bounds().size.width) },
                        }, handler, cx)
                            .when(view.selected.is_empty() && pot_ui::onboarding::missing_desktop(m), |el| {
                                el.child(ui::card(cx).mt_2()
                                    .child(crate::application::i18n::text("onboarding.desktop-failure"))
                                    .child(div().flex().flex_wrap().gap_2()
                                        .when(platform::with(|p| p.capabilities().sandbox_creation), |el| {
                                            el.child(Button::new(SharedString::from(format!("desktop-create-{}", m.id)))
                                            .ghost().small().label(crate::application::i18n::text("onboarding.create-desktop"))
                                            .on_click(|_, _, _| ui::command(serde_json::json!({"action":"sandbox-onboarding"}))))
                                        })
                                        .child(Button::new(SharedString::from(format!("desktop-connect-{}", m.id)))
                                            .ghost().small().label(crate::application::i18n::text("onboarding.connect-desktop"))
                                            .on_click(cx.listener(|this, _, _, cx| {
                                                this.machine_connections.connect();
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
                                            .accessibility_label(crate::application::i18n::tr!("main.open-task", task.title, task.progress))
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
                                    .when(view.running_total > 3, |el| el.child(Button::new("running-overflow").ghost().small().text_color(rgb(muted)).accessibility_label(crate::application::i18n::tr!("main.more-running-view-tasks", view.running_total-3)).child(div().w_full().text_size(px(12.)).font_weight(FontWeight::NORMAL).child(crate::application::i18n::tr!("main.more-running", view.running_total-3)))
                                        .on_click(cx.listener(|this, _, window, cx| { this.task_search.update(cx, |field, cx| field.set_value("", window, cx)); ui::command(serde_json::json!({"action":"page", "value":3})); this.show_tasks=true; this.schedule_tab=false; this.client.filter_tasks("active".into(), String::new(), 0); cx.notify(); })))))
                            })
                    }))
                    .child(self.collaboration_updates(&view,cx)),
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
                        .when_some(self.host["sandbox_error"].as_str().filter(|error| !error.is_empty()), |el, error| {
                            el.child(div().py_2().child(error.to_owned()))
                        })
                        .when(running, |el| {
                            el.child(div().py_2().child(if stopping {
                                crate::application::i18n::text("main.stopping")
                            } else {
                                crate::application::i18n::text("main.pot-is-working")
                            }))
                        })
                        .when(
                            snapshot
                                .and_then(|s| s.budget.as_ref())
                                .is_some_and(|b| b.remaining() == 0),
                            |el| el.child(crate::application::i18n::text("main.request-allowance-exhausted-adjust-it-in-settings")),
                        )
                        .when(!at_bottom && !self.jumping_to_latest, |el| {
                            el.child(
                                Button::new("latest")
                                    .ghost()
                                    .w_full()
                                    .h(px(32.))
                                    .bg(rgb(bubble))
                                    .cursor_pointer()
                                    .label(crate::application::i18n::text("main.latest-messages"))
                                    .accessibility_label(crate::application::i18n::text("main.scroll-to-latest-messages"))
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
                    .h(px(self.composer.height+48.
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
        self.tick(cx);
        if let Some(search) = self.pending_search.take() {
            self.task_search
                .update(cx, |input, cx| input.set_value(search, window, cx));
        }
        self.composer
            .sync(&self.client.view.lock().unwrap(), window, cx);
        self.layout_composer(window, cx);
        if self.host["page"] != 2
            && self.host["page"] != 6
            && let Some((owner, selected)) = self.settings_return.take()
        {
            let destination = (self.show_tasks, self.schedule_tab);
            if self.activate_owner(&owner, window, cx) {
                self.client.select(selected);
            }
            (self.show_tasks, self.schedule_tab) = destination;
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
        self.project_route();
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
        let visible = window.fully_visible_bounds();
        div()
            .w(visible.size.width)
            .h(visible.size.height)
            .relative()
            .left(visible.origin.x)
            .top(visible.origin.y)
            .capture_key_down(cx.listener(Self::composer_key_down))
            .flex()
            .flex_col()
            .bg(cx.theme().background)
            .child(self.titlebar(&view, window, cx))
            .child(self.notification_banner(cx))
            .child(body)
            .child(self.rail(cx))
            .when(
                self.host["page"] == 2 && self.host["settings"]["retry"] == true,
                |el| {
                    el.child(
                        Button::new("configuration-retry")
                            .primary()
                            .small()
                            .absolute()
                            .right(px(32.))
                            .bottom(px(24.))
                            .label(i18n::text("ui.retry"))
                            .on_click(|_, _, _| {
                                ui::command(serde_json::json!({"action":"ai-retry"}))
                            }),
                    )
                },
            )
            .when(self.composer_visible(&view), |el| {
                el.child(self.composer_frame(window, cx))
            })
    }
}

fn task_phase(p: &pot_protocol::TaskPhase) -> &'static str {
    use pot_protocol::TaskPhase::*;
    match p {
        Queued => crate::application::i18n::text("schedules-ui.queued"),
        Waiting => crate::application::i18n::text("main.waiting-for-allowance"),
        Running => crate::application::i18n::text("schedules-ui.running"),
        Complete => crate::application::i18n::text("schedules-ui.completed"),
        Failed => crate::application::i18n::text("main.failed-add-instructions-to-continue"),
        Cancelled => crate::application::i18n::text("main.stopped"),
        Interrupted => {
            crate::application::i18n::text("main.interrupted-add-instructions-to-resume")
        }
    }
}

impl Shell {
    pub fn new(window: &mut Window, cx: &mut Context<Self>) -> Self {
        Self {
            page: 0,
            pending_search: None,
            configuration_assistant: None,
            brand: [
                std::sync::Arc::new(Image::from_bytes(
                    ImageFormat::Png,
                    include_bytes!("../../assets/PotCompanion.png").to_vec(),
                )),
                std::sync::Arc::new(Image::from_bytes(
                    ImageFormat::Png,
                    include_bytes!("../../assets/PotCooking.png").to_vec(),
                )),
            ],
            settings_state: Default::default(),
            composer: composer::Composer::new(window, cx),
            other_mode: None,
            assistant_list: true,
            space_directory: Default::default(),
            usage_location: if platform::with(|p| p.aggregates_spaces()) {
                String::new()
            } else {
                platform::mode()
            },
            settings_return: None,
            notifications: Vec::new(),
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
            bridge_projection: serde_json::Value::Null,
            route_projection: serde_json::Value::Null,
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
            host: serde_json::Value::Null,
            usage_ui: usage_ui::UsageUI::new(
                pot_ui::localization::Catalog::new(&i18n::locale()),
                window,
                cx,
            ),
            settings: ui::Settings::new(window, cx),
            schedule_tab: false,
            schedules_ui: schedules_ui::SchedulesUI::new(window, cx),
            task_search: {
                let input = cx.new(|cx| {
                    InputState::new(window, cx)
                        .placeholder(i18n::text("main.search-task-title-or-id-47"))
                });
                cx.observe(&input, |_, _, _| platform::with(|p| p.wake()))
                    .detach();
                input
            },
        }
    }
}

impl Shell {
    fn composer_visible(&self, view: &client::View) -> bool {
        !self.assistant_list
            && !self.show_tasks
            && [0, 3].contains(&self.host["page"].as_u64().unwrap_or(0))
            && !view
                .snapshot
                .as_ref()
                .is_some_and(|s| s.agent.phase == pot_protocol::AgentPhase::NeedsConfiguration)
    }
    fn rail(&self, cx: &mut Context<Self>) -> Div {
        let page = self.host["page"].as_u64().unwrap_or(0);
        let selected = if page == 6 {
            4
        } else if page == 5 {
            if self.client.view.lock().unwrap().selected.is_empty() {
                0
            } else {
                3
            }
        } else {
            page
        };
        div()
            .absolute()
            .left_0()
            .top(px(44.))
            .bottom_0()
            .w(px(56.))
            .bg(design::navigation(cx))
            .flex()
            .flex_col()
            .items_center()
            .pt(px(24.))
            .gap(px(12.))
            .children(
                [
                    (
                        0,
                        gpui_kit::assets::IconName::MessageCircle,
                        "navigation.chat",
                    ),
                    (3, gpui_kit::assets::IconName::Inbox, "navigation.tasks"),
                    (1, gpui_kit::assets::IconName::Monitor, "machines.title"),
                    (4, gpui_kit::assets::IconName::ChartPie, "navigation.usage"),
                ]
                .into_iter()
                .map(|(page, icon, key)| self.rail_button(page, icon, key, selected, cx)),
            )
            .child(div().flex_1())
            .child(self.rail_button(
                2,
                gpui_kit::assets::IconName::Settings,
                "ui.settings",
                selected,
                cx,
            ))
            .pb(px(16.))
    }
    fn rail_button(
        &self,
        page: u64,
        icon: gpui_kit::assets::IconName,
        key: &str,
        selected: u64,
        cx: &Context<Self>,
    ) -> Button {
        Button::new(("app-destination", page as usize))
            .ghost()
            .when(page != 0, |b| b.icon(icon))
            .when(page == 0, |b| {
                let cooking = self.client.view.lock().unwrap().is_cooking();
                let image =
                    div().child(img(self.brand[usize::from(cooking)].clone()).size(px(28.)));
                let image = if cooking && !cx.reduce_motion() {
                    image
                        .with_animation(
                            "pot-cooking",
                            Animation::new(std::time::Duration::from_millis(2700)).repeat(),
                            |el, t| el.opacity(0.825 + 0.175 * (t * std::f32::consts::TAU).cos()),
                        )
                        .into_any_element()
                } else {
                    image.into_any_element()
                };
                b.child(image)
                    .when(self.host["unread"].as_u64().unwrap_or(0) > 0, |b| {
                        b.child(
                            div()
                                .absolute()
                                .right(px(2.))
                                .top(px(2.))
                                .size(px(6.))
                                .rounded_full()
                                .bg(cx.theme().primary),
                        )
                    })
            })
            .w(px(36.))
            .h(px(36.))
            .rounded(px(10.))
            .toggled(selected == page)
            .accessibility_label(i18n::text(key))
            .tooltip(i18n::text(key))
            .on_click(cx.listener(move |this, _, _, cx| {
                if page == 0 {
                    this.assistant_list = true;
                    this.client.select(String::new());
                }
                this.show_tasks = page == 3;
                this.schedule_tab = false;
                ui::command(
                    serde_json::json!({"action":"page","value":page,"assistant_list":page==0}),
                );
                cx.notify();
            }))
    }
}

impl Shell {
    fn project_page(&mut self, page: u64) {
        self.page = page;
        self.host["page"] = serde_json::json!(page);
        self.project_route();
    }
    fn project_route(&mut self) {
        let view = self.client.view.lock().unwrap().clone();
        if view.runtime["runtime_id"].as_str().is_none() {
            return;
        }
        if [0, 3].contains(&self.page) && !self.show_tasks && !self.assistant_list {
            self.page = if view.selected.is_empty() { 0 } else { 3 };
            self.host["page"] = serde_json::json!(self.page);
        }
        let route = self.route(&view).browser_page(self.assistant_list);
        let projection = serde_json::json!({"action":"page","value":self.page,"route":route,"assistant":view.assistant_id(),"task":view.selected,"schedule":view.schedule_selected});
        if projection != self.route_projection {
            platform::with(|p| p.command(projection.clone()));
            self.route_projection = projection;
        }
    }
}
