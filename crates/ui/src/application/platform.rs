//! Platform services. Implementations live in the App and browser crates.
use super::{
    client::Client,
    spaces::{Directory, Owner},
};
use serde_json::Value;
use std::{
    cell::RefCell,
    collections::{BTreeMap, BTreeSet},
    rc::Rc,
    sync::{Arc, Mutex},
};
#[derive(Clone, Default)]
pub struct EditorSnapshot {
    pub text: String,
    pub caret: usize,
    pub selection: bool,
    pub composing: bool,
}
#[derive(Clone, Copy, Default)]
pub struct Capabilities {
    pub browser_access: bool,
    pub external_text_input: bool,
    pub command_key: bool,
    pub explicit_send_retry: bool,
    pub local_execution_settings: bool,
    pub resource_cache: bool,
    pub sandbox_creation: bool,
    pub control_switching: bool,
}
pub trait Platform {
    fn capabilities(&self) -> Capabilities;

    fn client(&self, mode: &str) -> Client;
    fn mode(&self) -> String;
    fn toggle_mode(&self) -> Result<(), String>;
    fn select_assistant(&self, id: &str);
    fn control_projection(&self) -> Value;
    fn directory(&self) -> Arc<Mutex<Directory>>;
    fn refresh_directory(&self);
    fn changes(&self) -> BTreeMap<String, BTreeSet<String>> {
        BTreeMap::new()
    }
    fn command(&self, value: Value);
    fn request(&self, request: super::settings_state::Request);
    fn events(&self) -> Vec<Value>;
    fn state(&self) -> Value;
    fn project(&self, value: &Value);
    fn dark(&self) -> bool;
    fn reduce_motion(&self) -> bool;
    fn watch_machines(&self, _visible: bool) {}
    fn permission(&self, owner: &Owner, value: Value);
    fn artifact(
        &self,
        _message: &pot_protocol::Message,
        _selected: &str,
    ) -> Option<(String, Value)> {
        None
    }
    fn wake(&self);
    fn aggregates_spaces(&self) -> bool {
        false
    }
    fn editor(&self) -> Option<EditorSnapshot> {
        None
    }
    fn is_composing(&self) -> bool {
        false
    }
    fn native_window(&self) -> bool {
        false
    }
}
thread_local! {static PLATFORM:RefCell<Option<Rc<dyn Platform>>> = RefCell::new(None);}
pub fn install(platform: Rc<dyn Platform>) {
    PLATFORM.with(|cell| *cell.borrow_mut() = Some(platform));
}
pub fn with<R>(f: impl FnOnce(&dyn Platform) -> R) -> R {
    let platform = PLATFORM.with(|cell| {
        cell.borrow()
            .as_ref()
            .expect("UI platform installed")
            .clone()
    });
    f(platform.as_ref())
}
pub fn mode() -> String {
    with(|p| p.mode())
}
pub fn toggle_mode() -> Result<(), String> {
    with(|p| p.toggle_mode())
}
pub fn select_assistant(id: &str) {
    with(|p| p.select_assistant(id))
}
pub fn control_projection() -> Value {
    with(|p| p.control_projection())
}
