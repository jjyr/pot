//! Window-local editing sessions. Return contexts never contain backend snapshots.
use super::{Shell, gpui, i18n, navigation, platform, ui};
use crate::pages::Control;
use gpui::{prelude::*, *};
use gpui_kit::component::button::{Button, ButtonVariants};
use serde_json::{Value, json};

#[derive(Clone, Copy, PartialEq, Eq)]
pub(super) enum Kind {
    Ai,
    Assistant,
    Machine,
    Schedule,
}
pub(super) struct Frame {
    pub kind: Kind,
    source: navigation::State,
    scope: (String, String),
    settings: Option<ui::Settings>,
    draft: super::settings_state::EditorDraft,
    focus: Option<FocusHandle>,
    baseline: Option<Value>,
    submitting: bool,
    dirty: bool,
}

// Only presentation/draft keys are restored. Live profiles, revisions and pending
// requests remain owned by SettingsState, including late replies and retries.

impl Shell {
    pub(super) fn begin_editor(&mut self, kind: Kind, window: &mut Window, cx: &mut Context<Self>) {
        self.navigation_open = false;
        self.menu_fraction = 0.;
        self.menu_animation = None;
        let dirty = self
            .editors
            .last()
            .and_then(|frame| frame.baseline.as_ref())
            .is_some_and(|baseline| *baseline != self.editor_values(cx));
        if let Some(frame) = self.editors.last_mut() {
            frame.dirty = dirty;
        }
        let view = self.client.view.lock().unwrap();
        let scope = (
            view.client_mode.clone(),
            view.runtime["runtime_id"]
                .as_str()
                .unwrap_or_default()
                .to_owned(),
        );
        drop(view);
        let source = self.capture_navigation();
        let draft = self.settings_state.capture_editor();
        let settings = (kind == Kind::Ai)
            .then(|| std::mem::replace(&mut self.settings, ui::Settings::new(window, cx)));
        self.editors.push(Frame {
            kind,
            source,
            scope,
            settings,
            draft,
            focus: (window.fully_visible_bounds().size.width >= px(720.))
                .then(|| window.focused(cx))
                .flatten(),
            baseline: None,
            submitting: false,
            dirty: false,
        });
        self.editor_history();
        cx.notify();
    }
    fn editor_history(&self) {
        platform::with(|p| p.command(json!({"action":"editor-depth","depth":self.editors.len()})));
    }
    pub(super) fn open_assistant_editor(
        &mut self,
        owner: &str,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        if !self
            .editors
            .last()
            .is_some_and(|frame| frame.kind == Kind::Assistant)
        {
            self.begin_editor(Kind::Assistant, window, cx);
        }
        self.assistants_ui.form_only = true;
        self.assistants_ui.select(owner);
        if owner == "new" {
            self.assistant_list = true;
        }
        cx.notify();
    }
    pub(super) fn close_assistant_editor(&mut self, cx: &mut Context<Self>) {
        if self
            .editors
            .last()
            .is_some_and(|frame| frame.kind == Kind::Assistant)
        {
            self.editor_intents.push(json!({"action":"editor-back"}));
        } else {
            self.assistants_ui.open = false;
        }
        cx.notify();
    }
    pub(super) fn open_machine_editor(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.begin_editor(Kind::Machine, window, cx);
        self.machine_connections.connect();
        self.project_page(1);
    }
    pub(super) fn editor_title(&self) -> Option<String> {
        Some(match self.editors.last()?.kind {
            Kind::Ai => {
                let state = &self.settings_state.state;
                i18n::text(match state["ai_screen"].as_str().unwrap_or("main") {
                    "add" => "ai.connect-agent-58",
                    "manage" => "ai.configure",
                    "main-edit" => "ai.main-agent",
                    _ => "ui.settings",
                })
                .to_owned()
            }
            Kind::Assistant => i18n::text("spaces.edit-assistant").to_owned(),
            Kind::Machine => self.machine_connections.title(self.catalog()).to_owned(),
            Kind::Schedule => i18n::text("schedules-ui.edit-schedule").to_owned(),
        })
    }
    fn editor_values(&self, cx: &App) -> Value {
        match self.editors.last().map(|frame| frame.kind) {
            Some(Kind::Ai) => self.settings.editor_values(&self.settings_state.state, cx),
            Some(Kind::Assistant) => self.assistants_ui.editor_values(cx),
            Some(Kind::Machine) => self.machine_connections.editor_values(cx),
            Some(Kind::Schedule) => self.schedules_ui.editor_values(cx),
            None => Value::Null,
        }
    }
    pub(super) fn observe_editor(&mut self, cx: &App) {
        let values = self.editor_values(cx);
        if values.is_null()
            && self
                .editors
                .last()
                .is_some_and(|frame| frame.kind == Kind::Assistant)
        {
            return;
        }
        let machine_pending = self.client.view.lock().unwrap().machine_pending;
        if let Some(frame) = self.editors.last_mut() {
            frame.baseline.get_or_insert(values);
            frame.submitting |= frame.kind == Kind::Machine && machine_pending;
        }
    }
    fn editor_busy(&self) -> bool {
        let view = self.client.view.lock().unwrap();
        match self.editors.last().map(|frame| frame.kind) {
            Some(Kind::Ai) => {
                self.settings_state.state["ai_busy"] == true
                    || self.settings_state.state["retry"] == true
            }
            Some(Kind::Assistant) => view.assistant_pending,
            Some(Kind::Machine) => view.machine_pending,
            Some(Kind::Schedule) => view.schedule_pending,
            None => false,
        }
    }
    fn guard_editor_leave(&mut self, intent: &Value, cx: &App) -> bool {
        if self.editors.is_empty() {
            return true;
        }
        if self.editor_busy() {
            return false;
        }
        let back = intent["action"] == "editor-back" || intent["value"]["action"] == "ai-back";
        let leaves_parent = !back || intent["levels"].as_u64().unwrap_or(1) > 1;
        if (leaves_parent && self.editors.iter().any(|frame| frame.dirty))
            || self
                .editors
                .last()
                .and_then(|frame| frame.baseline.as_ref())
                .is_some_and(|baseline| *baseline != self.editor_values(cx))
        {
            self.editor_leave = Some(intent.clone());
            return false;
        }
        true
    }
    fn finish_editor(
        &mut self,
        levels: usize,
        saved: bool,
        window: &mut Window,
        cx: &mut Context<Self>,
    ) {
        self.editor_leave = None;
        let mut remaining = levels;
        if remaining == 0 {
            return;
        }
        while let Some(frame) = self.editors.pop() {
            match frame.kind {
                Kind::Assistant => self.assistants_ui.close(!saved),
                Kind::Machine => self.machine_connections.close(),
                Kind::Schedule => self.schedules_ui.open = false,
                Kind::Ai => {
                    if let Some(settings) = frame.settings {
                        self.settings = settings;
                    }
                    self.settings_state.restore_editor(frame.draft);
                    // Retained field entities already hold the caller's unsaved text.
                    self.settings.adopt_revision(&self.settings_state.state);
                }
            }
            let view = self.client.view.lock().unwrap();
            let same_scope = frame.scope.0 == view.client_mode
                && frame.scope.1 == view.runtime["runtime_id"].as_str().unwrap_or_default();
            drop(view);
            if same_scope {
                self.restore_navigation(frame.source, window, cx);
                if let Some(focus) = frame.focus {
                    focus.focus(window, cx);
                }
            } else {
                self.client.view.lock().unwrap().command_error =
                    i18n::text("spaces.identity-changed").into();
            }
            remaining -= 1;
            if remaining == 0 {
                break;
            }
        }
        if saved {
            self.settings_state.state["result"] = i18n::text("editor.saved").into();
        }
        self.editor_history();
        self.host["settings"] = self.settings_state.projection(
            &platform::with(|p| p.state())["settings"],
            platform::with(|p| p.capabilities().local_execution_settings),
        );
        cx.notify();
    }
    pub(super) fn process_editor_intents(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let machine_saved = {
            let view = self.client.view.lock().unwrap();
            self.editors
                .last()
                .is_some_and(|frame| frame.kind == Kind::Machine && frame.submitting)
                && !view.machine_pending
                && view.machine_reply["error"].is_null()
                && matches!(
                    view.machine_reply["intent"]["command"]["action"].as_str(),
                    Some("set_tags" | "configure_control")
                )
        };
        if machine_saved {
            self.finish_editor(1, true, window, cx);
        }
        // Successful replies may arrive during the host's tick, before a render.
        if self.settings_state.state["editor_saved"] == true {
            self.settings_state.state["editor_saved"] = false.into();
            if self.editors.iter().any(|frame| frame.kind == Kind::Ai) {
                let levels = self
                    .editors
                    .iter()
                    .rev()
                    .take_while(|frame| frame.kind == Kind::Ai)
                    .count();
                self.finish_editor(levels, true, window, cx);
            }
        }
        if self
            .editors
            .last()
            .is_some_and(|frame| frame.kind == Kind::Schedule)
            && !self.schedules_ui.open
        {
            self.finish_editor(1, true, window, cx);
        }
        for mut event in std::mem::take(&mut self.editor_intents) {
            if self.editor_leave.is_some()
                && !matches!(
                    event["action"].as_str(),
                    Some("editor-stay" | "editor-discard")
                )
            {
                continue;
            }
            let mut confirmed = false;
            match event["action"].as_str() {
                Some("assistant-editor-saved") => {
                    if self
                        .editors
                        .last()
                        .is_some_and(|frame| frame.kind == Kind::Assistant)
                    {
                        self.finish_editor(1, true, window, cx);
                    }
                    continue;
                }
                Some("editor-stay") => {
                    self.editor_leave = None;
                    if let Some(focus) = self.editor_resume_focus.take() {
                        focus.focus(window, cx);
                    }
                    cx.notify();
                    continue;
                }
                Some("editor-discard") => {
                    let Some(pending) = self.editor_leave.take() else {
                        continue;
                    };
                    event = pending;
                    confirmed = true;
                }
                _ => {}
            }
            let action = event["action"].as_str().unwrap_or("");
            let config = action == "configuration-command";
            let value = if config {
                event["value"].clone()
            } else {
                event.clone()
            };
            let command = value["action"].as_str().unwrap_or("");
            let back = action == "editor-back" || (config && command == "ai-back");
            let navigate = matches!(action, "navigation-command" | "navigation");
            if (back || navigate || (config && command == "section"))
                && !confirmed
                && !self.guard_editor_leave(&event, cx)
            {
                if self.editor_leave.is_some() {
                    self.editor_resume_focus = window.focused(cx);
                    self.editor_prompt_focus.focus(window, cx);
                }
                cx.notify();
                continue;
            }
            if back {
                if !self.editors.is_empty() {
                    let levels = event["levels"].as_u64().unwrap_or(1) as usize;
                    self.finish_editor(levels, false, window, cx);
                } else {
                    self.navigate_up(window, cx);
                }
                continue;
            }
            if navigate {
                if !self.editors.is_empty() {
                    self.finish_editor(usize::MAX, false, window, cx);
                }
                let page = if action == "navigation-command" {
                    event["value"]["value"].as_u64()
                } else {
                    event["value"].as_u64()
                }
                .unwrap_or(0);
                if page == 2 {
                    self.settings_state.state["return_to_chat"] = false.into();
                }
                if action == "navigation" || page != 0 {
                    self.assistant_overview = page == 0 && self.narrow;
                } else if event["value"]["route"] == "chat" {
                    self.assistant_overview = false;
                }
                if page == 0 {
                    self.assistant_list =
                        !self.client.view.lock().unwrap().assistants["assistants"]
                            .as_array()
                            .is_some_and(|a| !a.is_empty());
                    self.client.select(String::new());
                }
                self.show_tasks = page == 3;
                if action == "navigation" {
                    self.schedule_tab = false;
                }
                self.project_page(page);
            } else if config {
                if command == "section" && !self.editors.is_empty() {
                    self.finish_editor(usize::MAX, false, window, cx);
                }
                let enters = matches!(command, "ai-open" | "ai-new" | "ai-manage" | "task-profile")
                    || (command == "ai-screen" && value["screen"] != "main");
                if enters
                    && (self.settings_state.state["ai_busy"] != true
                        || matches!(command, "ai-open" | "task-profile"))
                {
                    self.begin_editor(Kind::Ai, window, cx);
                }
                if command == "ai-open"
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
                if matches!(command, "ai-open" | "task-profile") {
                    self.project_page(2);
                }
                if command == "ai-save-connection" {
                    self.settings.reveal_connection_error(&value, window, cx);
                }
                for request in self.settings_state.command(&value) {
                    platform::with(|p| p.request(request));
                }
            }
            self.host["settings"] = self.settings_state.projection(
                &platform::with(|p| p.state())["settings"],
                platform::with(|p| p.capabilities().local_execution_settings),
            );
            cx.notify();
        }
    }
    pub(super) fn editor_leave_prompt(&self, cx: &Context<Self>) -> Div {
        let actions = div()
            .flex()
            .flex_wrap()
            .justify_end()
            .gap_2()
            .child(
                Button::new("editor-stay")
                    .control(cx)
                    .primary()
                    .label(i18n::text("editor.keep-editing"))
                    .on_click(|_, _, _| {
                        platform::with(|p| p.command(json!({"action":"editor-stay"})))
                    }),
            )
            .child(
                Button::new("editor-discard")
                    .control(cx)
                    .label(i18n::text("editor.discard"))
                    .on_click(|_, _, _| {
                        platform::with(|p| p.command(json!({"action":"editor-discard"})))
                    }),
            );
        div()
            .absolute()
            .inset_0()
            .occlude()
            .track_focus(&self.editor_prompt_focus)
            .on_key_down(|event, _, cx| {
                if matches!(event.keystroke.key.as_str(), "escape" | "enter") {
                    platform::with(|p| p.command(json!({"action":"editor-stay"})));
                    cx.stop_propagation();
                }
            })
            .bg(gpui::black().opacity(0.25))
            .flex()
            .items_center()
            .justify_center()
            .p_4()
            .child(
                crate::pages::card(cx)
                    .max_w(px(420.))
                    .child(i18n::text("editor.discard-prompt"))
                    .child(actions),
            )
    }
}
