use pot_ui::application::{
    client::{Client, Driver},
    platform::Platform,
    spaces::{Directory, Owner},
};
use serde_json::Value;
use std::{
    cell::RefCell,
    ffi::{CStr, CString},
    rc::Rc,
    sync::{Arc, Mutex},
};
impl Driver for crate::client::Client {
    fn send(&self, text: String) -> bool {
        self.send(text)
    }
    fn dispatch(&self, kind: &str, v: Value) {
        match kind {
            "refresh" => self.refresh(),
            "cancel" => self.cancel(),
            "select" => self.select(v.as_str().unwrap_or_default().into()),
            "select-assistant" => self.select_assistant(v.as_str().unwrap_or("main").into()),
            "permission" => self.permission_decision(v),
            "sandbox-lifecycle" => self.sandbox_lifecycle(v),
            "usage" => self.usage_action(
                match v["path"].as_str() {
                    Some("policy") => "policy",
                    Some("resume") => "resume",
                    _ => "cancel",
                },
                v["body"].clone(),
            ),
            "schedule-select" => self.schedule_select(v.as_str().unwrap_or_default().into()),
            "schedule" => self.schedule_action(v),
            "machine" => self.machine_command(v),
            "assistant-save" => self.assistant_save(v),
            "execution-target" => {
                self.filter_execution_target(v.as_str().unwrap_or_default().into())
            }
            "tasks" => self.filter_tasks(
                v["scope"].as_str().unwrap_or("active").into(),
                v["q"].as_str().unwrap_or_default().into(),
                v["offset"].as_u64().unwrap_or(0) as usize,
            ),
            _ => {}
        }
    }
}
pub struct Native {
    state: RefCell<Value>,
    events: Arc<Mutex<Vec<Value>>>,
    watches: RefCell<std::collections::BTreeMap<String, crate::backend::Subscription>>,
}
impl Native {
    pub fn new() -> Self {
        Self {
            state: RefCell::new(Value::Null),
            events: Default::default(),
            watches: Default::default(),
        }
    }
}
fn read_ui() -> Value {
    let mut buffer = vec![0i8; 524288];
    if unsafe { crate::pot_bridge_ui(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
        serde_json::from_slice(unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes())
            .unwrap_or_default()
    } else {
        Value::Null
    }
}
impl Platform for Native {
    fn aggregates_spaces(&self) -> bool {
        true
    }
    fn capabilities(&self) -> pot_ui::application::platform::Capabilities {
        pot_ui::application::platform::Capabilities {
            browser_access: false,
            external_text_input: true,
            command_key: true,
            explicit_send_retry: false,
            local_execution_settings: crate::backend::mode() == "local",
            resource_cache: true,
            sandbox_creation: true,
            control_switching: true,
        }
    }
    fn client(&self, mode: &str) -> Client {
        let driver = Rc::new(crate::client::Client::start_mode(mode));
        Client::new(driver.view.clone(), driver)
    }
    fn mode(&self) -> String {
        crate::backend::mode().into()
    }
    fn toggle_mode(&self) -> Result<(), String> {
        crate::backend::toggle_mode()
    }
    fn select_assistant(&self, id: &str) {
        crate::backend::select_assistant(id)
    }
    fn control_projection(&self) -> Value {
        crate::backend::control_projection()
    }
    fn directory(&self) -> Arc<Mutex<Directory>> {
        crate::spaces::directory()
    }
    fn refresh_directory(&self) {
        crate::spaces::refresh_directory()
    }
    fn changes(&self) -> std::collections::BTreeMap<String, std::collections::BTreeSet<String>> {
        crate::spaces::take_native_changes()
    }
    fn command(&self, v: Value) {
        if matches!(
            v["action"].as_str(),
            Some(
                "configuration-command"
                    | "navigation-command"
                    | "editor-back"
                    | "editor-stay"
                    | "editor-discard"
            )
        ) {
            self.events.lock().unwrap().push(v);
            crate::wake_ui();
            return;
        }
        if v["action"] == "editor-depth" {
            return;
        }
        if v["action"] == "reveal-file" {
            if let Some(path) = v["path"].as_str() {
                let _ = std::process::Command::new("open")
                    .arg("-R")
                    .arg(path)
                    .spawn();
            }
            return;
        }
        if let Ok(json) = CString::new(v.to_string()) {
            unsafe { crate::pot_bridge_command(json.as_ptr()) };
        }
    }
    fn events(&self) -> Vec<Value> {
        let mut events = std::mem::take(&mut *self.events.lock().unwrap());
        let mut buffer = vec![0i8; 524288];
        while unsafe { crate::pot_bridge_event(buffer.as_mut_ptr(), buffer.len() as i32) } > 0 {
            events.push(
                serde_json::from_slice(unsafe { CStr::from_ptr(buffer.as_ptr()) }.to_bytes())
                    .unwrap_or_default(),
            );
        }
        events
    }
    fn request(&self, request: pot_ui::application::settings_state::Request) {
        let events = self.events.clone();
        std::thread::spawn(move || {
            let result = crate::backend::configuration_request(
                &request.mode,
                &request.assistant,
                &request.control,
                &request.path,
                request.body,
            );
            let mut reply = serde_json::json!({"action":"configuration-reply","id":request.id,"context":request.context});
            match result {
                Ok(mut response) => {
                    let rejected = response.status().is_client_error();
                    let failed = rejected || response.status().is_server_error();
                    match response.body_mut().read_json::<Value>() {
                        Ok(body) if !failed => reply["body"] = body,
                        Ok(body) => {
                            reply["error"] = body["message"]
                                .as_str()
                                .or(body["code"].as_str())
                                .unwrap_or("Configuration request rejected")
                                .into();
                            reply["rejected"] = serde_json::json!(rejected);
                        }
                        Err(error) => reply["error"] = error.to_string().into(),
                    }
                }
                Err(error) => reply["error"] = error.into(),
            }
            events.lock().unwrap().push(reply);
            crate::wake_ui();
        });
    }

    fn state(&self) -> Value {
        let state = read_ui();
        *self.state.borrow_mut() = state.clone();
        state
    }
    fn project(&self, v: &Value) {
        if let Ok(json) = CString::new(v.to_string()) {
            unsafe { crate::pot_bridge_update(json.as_ptr()) };
        }
    }
    fn dark(&self) -> bool {
        unsafe { crate::pot_bridge_dark() }
    }
    fn reduce_motion(&self) -> bool {
        unsafe { crate::pot_bridge_reduce_motion() }
    }
    fn editor(&self) -> Option<pot_ui::application::platform::EditorSnapshot> {
        let state = self.state.borrow();
        if state.is_null() {
            return None;
        }
        let e = &state["editor"];
        Some(pot_ui::application::platform::EditorSnapshot {
            content_height: None,
            text: e["text"].as_str().unwrap_or_default().into(),
            caret: e["caret"].as_u64().unwrap_or(0) as usize,
            selection: e["selection"] == true,
            composing: e["composing"] == true,
        })
    }
    fn native_window(&self) -> bool {
        true
    }
    fn watch_machines(&self, visible: bool) {
        let modes = if !visible {
            vec![]
        } else if crate::backend::control_projection()["configured"] == true {
            vec!["local", "remote"]
        } else {
            vec!["local"]
        };
        let mut watches = self.watches.borrow_mut();
        watches.retain(|m, _| modes.contains(&m.as_str()));
        for mode in modes {
            watches.entry(mode.into()).or_insert_with(|| {
                crate::backend::subscribe(mode.into(), "/api/v1/machines/watch".into(), |_| Ok(()))
            });
        }
    }
    fn permission(&self, owner: &Owner, value: Value) {
        let owner = owner.clone();
        std::thread::spawn(move || {
            let result = crate::backend::Requests::for_mode(&owner.mode)
                .scope(&owner.assistant)
                .post("/api/v1/permissions")
                .send_json(value);
            if let Err(error) = result
                && let Some(s) = crate::spaces::directory()
                    .lock()
                    .unwrap()
                    .spaces
                    .get_mut(&owner.mode)
            {
                s.error = error;
            }
            crate::wake_ui();
        });
    }
    fn artifact(&self, message: &pot_protocol::Message, selected: &str) -> Option<(String, Value)> {
        let root = crate::client::root().join("workspace/workspace");
        let root = if selected.is_empty() {
            root
        } else {
            root.join("tasks").join(selected)
        };
        let path = crate::artifacts::from_message(message, &root)?;
        Some((
            path.file_name()?.to_string_lossy().into(),
            serde_json::json!({"action":"reveal-file","path":path}),
        ))
    }
    fn wake(&self) {
        crate::wake_ui()
    }
}
