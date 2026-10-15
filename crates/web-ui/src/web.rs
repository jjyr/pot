//! Browser platform adapter. All product presentation lives in pot-ui.
use gpui_kit::{prelude::*, *};
use pot_ui::application::{
    Shell,
    client::{Client, Driver, View},
    platform::{self, Platform},
    spaces::{Directory, Owner, Space},
};
use serde_json::{Value, json};
use std::{
    borrow::Cow,
    cell::RefCell,
    rc::Rc,
    sync::{Arc, Mutex},
};
use wasm_bindgen::prelude::*;
#[wasm_bindgen]
extern "C" {
    #[wasm_bindgen(js_namespace=window,js_name=potAction)]
    fn action(name: &str, value: &str);
    #[wasm_bindgen(js_namespace=window,js_name=potIsComposing)]
    fn is_composing() -> bool;
    #[wasm_bindgen(js_namespace=window,js_name=potCommandKey)]
    fn command_key() -> bool;
}
thread_local! {
 static SEND:RefCell<pot_ui::state::SendState>=RefCell::new(Default::default());
 static VIEW:RefCell<Option<Entity<Shell>>>=const{RefCell::new(None)};
 static HANDLE:RefCell<Option<ApplicationHandle>>=const{RefCell::new(None)};
 static HOST:RefCell<Option<Rc<Browser>>>=const{RefCell::new(None)};
 static PENDING:RefCell<Option<Value>>=const{RefCell::new(None)};
}
struct Browser {
    view: Arc<Mutex<View>>,
    directory: Arc<Mutex<Directory>>,
    state: RefCell<Value>,
    events: RefCell<Vec<Value>>,
    dark: RefCell<bool>,
    reduce: RefCell<bool>,
    route: RefCell<String>,
    receipt: RefCell<String>,
    assistant_sequence: RefCell<u64>,
}
impl Browser {
    fn new() -> Self {
        Self {
            view: Default::default(),
            directory: Default::default(),
            state: RefCell::new(json!({"page":0,"visible":true,"settings":{"locale":"en"}})),
            events: Default::default(),
            dark: Default::default(),
            reduce: Default::default(),
            route: Default::default(),
            receipt: Default::default(),
            assistant_sequence: Default::default(),
        }
    }
    fn publish(&self, data: Value) {
        let page = data["page"].as_str().unwrap_or("chat");
        let selected = data["selected"].as_str().unwrap_or_default();
        let route = format!(
            "{page}/{selected}/{}/{}",
            data["assistant_id"].as_str().unwrap_or("main"),
            data["schedule_selected"].as_str().unwrap_or_default()
        );
        if *self.route.borrow() != route {
            *self.route.borrow_mut() = route;
            self.events
                .borrow_mut()
                .push(json!({"action":"route","page":page}));
        }
        let mut view = self.view.lock().unwrap();
        view.client_mode = "web".into();
        view.set_runtime(data["runtime"].clone());
        view.assistants = data["assistants"].clone();
        view.pending = false;
        view.select_assistant(data["assistant_id"].as_str().unwrap_or("main").into());
        view.select(selected.into());
        if let Ok(snapshot) =
            serde_json::from_value::<pot_protocol::Snapshot>(data["snapshot"].clone())
        {
            let expected = if selected.is_empty() {
                view.assistant_id()
            } else {
                selected
            };
            if snapshot.conversation_id == expected {
                match view.replace(snapshot) {
                    Ok(()) => {
                        if selected.is_empty() {
                            view.main_snapshot = view.snapshot.clone();
                        }
                    }
                    Err(error) => view.error = error,
                }
            }
        }
        if data["connected"] != true {
            view.error = data["notice"].as_str().unwrap_or_default().into()
        };
        view.command_error = data["command_error"].as_str().unwrap_or_default().into();
        view.pending = SEND.with(|s| s.borrow().pending().is_some());
        view.assistant_pending = data["assistant_pending"] == true;
        if !data["machines"].is_null() {
            view.machines = data["machines"].clone();
        }
        if !data["completions"].is_null() {
            view.completions = data["completions"].clone();
        }
        if !data["permissions"].is_null() {
            view.permissions = data["permissions"].clone();
        }
        if !data["assistant_rules"].is_null() {
            view.assistant_rules = data["assistant_rules"].clone();
        }
        if !data["usage"].is_null() {
            view.usage = data["usage"].clone();
        }
        if !data["machine_reply"].is_null() {
            view.machine_reply = data["machine_reply"].clone();
        }
        if let Ok(executing) =
            serde_json::from_value::<pot_protocol::TaskPage>(data["executing"].clone())
        {
            view.running_tasks.clear();
            let mut finished = 0;
            for task in executing.items {
                let snapshot = serde_json::from_value::<pot_protocol::Snapshot>(
                    data["running_snapshots"][&task.id].clone(),
                )
                .ok();
                if snapshot.as_ref().is_some_and(|s| s.turn.is_none()) {
                    finished += 1;
                    continue;
                }
                view.running_tasks
                    .push(pot_ui::application::client::RunningTask {
                        id: task.id,
                        title: task.title,
                        executor_icon: task.executor_icon,
                        progress: snapshot
                            .as_ref()
                            .map(pot_ui::application::client::task_progress)
                            .unwrap_or_else(|| {
                                pot_ui::application::i18n::text("client.working").into()
                            }),
                    });
            }
            view.running_total = executing.total.saturating_sub(finished);
        }
        view.machine_pending = data["machine_pending"] == true;
        if let Ok(q) = serde_json::from_value::<pot_protocol::TaskQuery>(data["task_query"].clone())
        {
            if q.q != view.task_query.q {
                self.events
                    .borrow_mut()
                    .push(json!({"action":"task-query-projection","q":q.q}));
            }
            view.task_query = q;
        }
        if let Ok(tasks) =
            serde_json::from_value::<pot_protocol::TaskPage>(data["task_page"].clone())
        {
            view.selected_task = tasks.selected.clone();
            view.tasks = tasks.items.clone();
            view.task_page = tasks;
        }
        if let Ok(schedules) = serde_json::from_value(data["schedules"].clone()) {
            view.schedules = schedules;
        }
        if let Some(selected) = data["schedule_selected"].as_str() {
            view.schedule_selected = selected.into();
            view.schedule_offset = data["schedule_offset"].as_u64().unwrap_or(0) as usize;
            view.runs_offset = data["runs_offset"].as_u64().unwrap_or(0) as usize;
        }
        view.schedule_pending = data["schedule_pending"] == true;
        view.schedule_error = data["schedule_error"].as_str().unwrap_or_default().into();
        view.schedule_success = data["schedule_success"].as_u64().unwrap_or(0);
        if let Ok(receipt) =
            serde_json::from_value::<pot_ui::state::Receipt>(data["accepted"].clone())
        {
            if *self.receipt.borrow() != receipt.id {
                *self.receipt.borrow_mut() = receipt.id;
                view.accepted_text = Some(receipt.text);
                view.accepted_conversation = Some(receipt.conversation);
            }
        }
        if let Some(owner) = data["assistant_saved_owner"].as_str() {
            let sequence = data["assistant_save_sequence"].as_u64().unwrap_or(0);
            if *self.assistant_sequence.borrow() != sequence {
                *self.assistant_sequence.borrow_mut() = sequence;
                view.assistant_saved_owner = Some(owner.into());
                view.created_assistant = data["created_assistant"].as_str().map(str::to_owned);
            }
        }
        let mut space = Space {
            mode: "web".into(),
            id: view.runtime["runtime_id"]
                .as_str()
                .unwrap_or("control")
                .into(),
            name: view.runtime["name"].as_str().unwrap_or("Control").into(),
            assistants: view.assistants.clone(),
            machines: view.machines.clone(),
            tasks: view.tasks.clone(),
            schedules: view
                .schedules
                .items
                .iter()
                .map(|s| (view.assistant_id().into(), s.clone()))
                .collect(),
            usage: view.usage.clone(),
            connected: data["connected"] == true,
            error: view.error.clone(),
            ..Default::default()
        };
        if let Ok(tasks) = serde_json::from_value(data["directory_tasks"].clone()) {
            space.tasks = tasks;
        }
        space.permissions =
            serde_json::from_value(data["space_permissions"].clone()).unwrap_or_default();
        space
            .permissions
            .insert(view.assistant_id().into(), view.permissions.clone());
        if let Ok(schedules) = serde_json::from_value(data["space_schedules"].clone()) {
            space.schedules = schedules;
        }
        if let Ok(records) = serde_json::from_value(data["assistant_mail"].clone()) {
            space.mail.insert(view.assistant_id().into(), records);
        }
        let mut directory = self.directory.lock().unwrap();
        directory.spaces.insert("web".into(), space);
        *self.dark.borrow_mut() = data["dark"] == true;
        *self.reduce.borrow_mut() = data["reduce_motion"] == true;
        let mut state = self.state.borrow_mut();
        state["page"] = json!(match page {
            "machines" => 1,
            "settings" => 2,
            "tasks" | "schedules" | "task" => 3,
            "usage" => 4,
            "conversation-usage" => 5,
            "quota" => 6,
            _ => 0,
        });
        state["timezone"] = data["timezone"].clone();
        state["command_key"] = data["command_key"].clone();
        if !data["settings"].is_null() {
            state["settings"] = data["settings"].clone();
        }
        state["settings"]["locale"] = data["locale"].clone();
    }
}
struct BrowserDriver {
    host: Rc<Browser>,
}
impl Driver for BrowserDriver {
    fn send(&self, text: String) -> bool {
        if self.host.view.lock().unwrap().can_send() {
            action("send", &text);
            true
        } else {
            false
        }
    }
    fn dispatch(&self, kind: &str, v: Value) {
        match kind {
            "select-assistant" => {
                let mut view = self.host.view.lock().unwrap();
                if view.select_assistant(v.as_str().unwrap_or("main").into()) {
                    action(
                        "selection",
                        &json!({"assistant":view.assistant_id(),"task":view.selected}).to_string(),
                    );
                }
            }
            "select" => {
                let mut view = self.host.view.lock().unwrap();
                if view.select(v.as_str().unwrap_or_default().into()) {
                    action(
                        "selection",
                        &json!({"assistant":view.assistant_id(),"task":view.selected}).to_string(),
                    );
                }
            }
            "retry" => action("retry", ""),
            "cancel" => action("stop", ""),
            "permission" => action("permission-decision", &v.to_string()),
            "sandbox-lifecycle" => action("sandbox-lifecycle", &v.to_string()),
            "tasks" => {
                let mut view = self.host.view.lock().unwrap();
                view.task_query.scope = v["scope"].as_str().unwrap_or("active").into();
                view.task_query.q = v["q"].as_str().unwrap_or_default().into();
                view.task_query.offset = v["offset"].as_u64().unwrap_or(0) as usize;
                action(
                    "task-query",
                    &serde_json::to_string(&view.task_query).unwrap(),
                );
            }
            "execution-target" => {
                self.host.view.lock().unwrap().task_query.execution_target =
                    v.as_str().unwrap_or_default().into();
                action("execution-target-tasks", v.as_str().unwrap_or_default());
            }
            "schedule-select" => {
                self.host.view.lock().unwrap().schedule_selected =
                    v.as_str().unwrap_or_default().into();
                action("schedule-select", v.as_str().unwrap_or_default());
            }
            "schedule" => action("schedule", &v.to_string()),
            "machine" => action("machine-command", &v.to_string()),
            "assistant-save" => action("assistant-save", &v.to_string()),
            "usage" => action("usage-action", &v.to_string()),
            "refresh" => {
                let view = self.host.view.lock().unwrap();
                if view.runtime["runtime_id"].as_str().is_none() {
                    return;
                }
                if let Some((scope, days, offset, timezone)) = &view.usage_query {
                    action(
                        "usage-query",
                        &json!({"scope":scope,"days":days,"offset":offset,"timezone":timezone})
                            .to_string(),
                    );
                }
                action("schedule-query",&json!({"selected":view.schedule_selected,"offset":view.schedule_offset,"runs_offset":view.runs_offset,"visible":view.schedules_visible}).to_string());
                action("refresh", "");
            }
            _ => {}
        }
    }
}
impl Platform for Browser {
    fn capabilities(&self) -> platform::Capabilities {
        platform::Capabilities {
            browser_access: true,
            command_key: self.state.borrow()["command_key"] == true,
            explicit_send_retry: true,
            ..Default::default()
        }
    }
    fn client(&self, _mode: &str) -> Client {
        HOST.with(|h| {
            Client::new(
                self.view.clone(),
                Rc::new(BrowserDriver {
                    host: h.borrow().as_ref().unwrap().clone(),
                }),
            )
        })
    }
    fn mode(&self) -> String {
        "web".into()
    }
    fn toggle_mode(&self) -> Result<(), String> {
        Err("This browser uses its configured Control".into())
    }
    fn select_assistant(&self, _id: &str) {}
    fn control_projection(&self) -> Value {
        json!({"configured":true,"mode":"web"})
    }
    fn directory(&self) -> Arc<Mutex<Directory>> {
        self.directory.clone()
    }
    fn refresh_directory(&self) {}
    fn command(&self, v: Value) {
        match v["action"].as_str().unwrap_or_default() {
            "configuration-command" | "navigation-command" => {
                self.events.borrow_mut().push(v);
                action("wake", "");
            }
            "page" => action("route", &v.to_string()),
            "execution-target-desktop" => action("desktop", v["id"].as_str().unwrap_or_default()),
            "file-download" => action("file-download", &v.to_string()),
            "section" | "history-section" => {
                self.state.borrow_mut()["settings"]["section"] = v["value"].clone();
                action("settings-command", &v.to_string());
            }
            "completion-active" | "machines-detail" | "mode-changed" | "backend-changed" => {}
            _ => action("settings-command", &v.to_string()),
        }
    }
    fn events(&self) -> Vec<Value> {
        std::mem::take(&mut *self.events.borrow_mut())
    }
    fn request(&self, request: pot_ui::application::settings_state::Request) {
        action("ui-request", &serde_json::to_string(&request).unwrap());
    }
    fn state(&self) -> Value {
        self.state.borrow().clone()
    }
    fn project(&self, _v: &Value) {}
    fn dark(&self) -> bool {
        *self.dark.borrow()
    }
    fn is_composing(&self) -> bool {
        is_composing()
    }
    fn reduce_motion(&self) -> bool {
        *self.reduce.borrow()
    }
    fn permission(&self, owner: &Owner, v: Value) {
        action(
            "permission-owner",
            &json!({"owner":owner.assistant,"body":v}).to_string(),
        );
    }
    fn wake(&self) {
        action("wake", "");
    }
}
#[wasm_bindgen]
pub fn ui_reply(reply: &str) {
    if let Ok(reply) = serde_json::from_str(reply) {
        HOST.with(|h| {
            if let Some(host) = h.borrow().as_ref() {
                host.events.borrow_mut().push(reply);
            }
        });
        action("wake", "");
    }
}
#[wasm_bindgen]
pub fn wake() {
    HANDLE.with(|cell| {
        if let Some(app) = cell.borrow().as_ref() {
            VIEW.with(|v| {
                if let Some(v) = v.borrow().as_ref() {
                    app.update(|cx| v.update(cx, |_, cx| cx.notify()));
                }
            });
        }
    });
}
#[wasm_bindgen]
pub fn update_view(data: &str) {
    let Ok(data) = serde_json::from_str(data) else {
        return;
    };
    HOST.with(|host| {
        if let Some(host) = host.borrow().as_ref() {
            host.publish(data);
        } else {
            PENDING.with(|pending| *pending.borrow_mut() = Some(data));
        }
    });
    action("wake", "");
}
#[wasm_bindgen]
pub fn run() {
    console_error_panic_hook::set_once();
    gpui_kit::platform::web_init();
    let web = Rc::new(
        gpui_kit::web::WebPlatform::new_with_backend_and_font_fallback(
            false,
            gpui_kit::web::WebBackendPreference::Auto,
            gpui_kit::web::CanvasFontFallback::EmojiAndCjk,
        ),
    );
    let client = Arc::new(web.fetch_http_client());
    let app = Application::with_platform(web)
        .with_assets(pot_ui::application::Assets)
        .with_http_client(client);
    let handle = app.run_embedded(|cx| {
        gpui_kit::init(cx);
        // WASM has no target OS. Adapt the input keymap to the browser's OS,
        // independently from the shared product shortcuts and presentation.
        if command_key() {
            use gpui_kit::base::input::*;
            cx.bind_keys([
                KeyBinding::new("cmd-a", SelectAll, Some("Input")),
                KeyBinding::new("cmd-c", Copy, Some("Input")),
                KeyBinding::new("cmd-x", Cut, Some("Input")),
                KeyBinding::new("cmd-v", Paste, Some("Input")),
                KeyBinding::new("cmd-z", Undo, Some("Input")),
                KeyBinding::new("cmd-shift-z", Redo, Some("Input")),
                KeyBinding::new("cmd-left", MoveHome, Some("Input")),
                KeyBinding::new("cmd-right", MoveEnd, Some("Input")),
                KeyBinding::new("cmd-up", MoveToStart, Some("Input")),
                KeyBinding::new("cmd-down", MoveToEnd, Some("Input")),
                KeyBinding::new("cmd-backspace", DeleteToBeginningOfLine, Some("Input")),
                KeyBinding::new("alt-backspace", DeleteToPreviousWordStart, Some("Input")),
                KeyBinding::new("alt-delete", DeleteToNextWordEnd, Some("Input")),
                KeyBinding::new("alt-left", MoveToPreviousWord, Some("Input")),
                KeyBinding::new("alt-right", MoveToNextWord, Some("Input")),
                KeyBinding::new("cmd-shift-left", SelectToStartOfLine, Some("Input")),
                KeyBinding::new("cmd-shift-right", SelectToEndOfLine, Some("Input")),
                KeyBinding::new("cmd-shift-up", SelectToStart, Some("Input")),
                KeyBinding::new("cmd-shift-down", SelectToEnd, Some("Input")),
                KeyBinding::new("alt-shift-left", SelectToPreviousWordStart, Some("Input")),
                KeyBinding::new("alt-shift-right", SelectToNextWordEnd, Some("Input")),
            ]);
        }
        pot_ui::design::install(cx);
        cx.text_system()
            .add_fonts(vec![Cow::Borrowed(
                include_bytes!("../fonts/IBMPlexSans-Regular.ttf").as_slice(),
            )])
            .expect("bundled font");
        let host = Rc::new(Browser::new());
        HOST.with(|h| *h.borrow_mut() = Some(host.clone()));
        PENDING.with(|pending| {
            if let Some(data) = pending.borrow_mut().take() {
                host.publish(data);
            }
        });
        platform::install(host);
        let (_, content) = gpui_kit::open_window(WindowOptions::default(), cx, |window, cx| {
            cx.new(|cx| Shell::new(window, cx))
        })
        .expect("browser window");
        VIEW.with(|v| *v.borrow_mut() = Some(content));
    });
    HANDLE.with(|h| *h.borrow_mut() = Some(handle));
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

#[wasm_bindgen]
pub fn apply_conversation_event(snapshot: &str, event: &str) -> Result<String, JsValue> {
    let mut snapshot: pot_protocol::Snapshot =
        serde_json::from_str(snapshot).map_err(|e| JsValue::from_str(&e.to_string()))?;
    let event = serde_json::from_str(event).map_err(|e| JsValue::from_str(&e.to_string()))?;
    pot_ui::state::apply_event(&mut snapshot, event).map_err(|e| JsValue::from_str(&e))?;
    serde_json::to_string(&snapshot).map_err(|e| JsValue::from_str(&e.to_string()))
}

#[wasm_bindgen]
pub fn reject_send(request_id: &str) {
    SEND.with(|s| s.borrow_mut().reject(request_id));
}
