//! Configuration state and request ownership shared by both hosts.
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::BTreeMap;
pub(super) fn provider_choices(state: &Value, query: &str) -> Vec<Value> {
    let query = query.trim().to_lowercase();
    state["directory"]
        .as_array()
        .into_iter()
        .flatten()
        .filter(|p| {
            if !query.is_empty() {
                return p["id"]
                    .as_str()
                    .unwrap_or("")
                    .to_lowercase()
                    .contains(&query)
                    || p["name"]
                        .as_str()
                        .unwrap_or("")
                        .to_lowercase()
                        .contains(&query);
            }
            p["common"] == true || state["more_providers"] == true
        })
        .cloned()
        .collect()
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Request {
    pub id: u64,
    pub mode: String,
    pub control: String,
    pub assistant: String,
    pub context: String,
    pub path: String,
    pub body: Option<Value>,
}
pub(super) struct EditorDraft {
    state: Value,
    profile: Value,
    draft_id: String,
    mode: String,
    control: String,
    assistant: String,
}
const DRAFT_KEYS: &[&str] = &[
    "section",
    "ai_screen",
    "return_to_chat",
    "routing_owner",
    "routing_owner_name",
    "connection_kind",
    "connection_profile_id",
    "connection_model",
    "connection_name",
    "connection_context",
    "connection_output",
    "connection_protocol_override",
    "api_endpoint",
    "api_protocol",
    "has_api_key",
    "draft_profile",
    "draft_model",
    "more_providers",
    "context_default",
    "output_default",
    "routing_profile",
    "result",
    "check_message",
    "model_options",
    "connection_draft_id",
];

#[derive(Default)]
pub struct SettingsState {
    pub state: Value,
    cached: BTreeMap<String, Box<SettingsState>>,
    context: String,
    mode: String,
    control: String,
    assistant: String,
    sequence: u64,
    pending: BTreeMap<u64, (&'static str, Request)>,
    loaded: bool,
    load_error: Option<String>,
    profile: Value,
    allowances: Vec<Value>,
    draft_id: String,
    after_save: Option<String>,
    activation: Option<String>,
    retry: Option<(&'static str, Request)>,
}
impl SettingsState {
    pub(super) fn capture_editor(&self) -> EditorDraft {
        EditorDraft {
            state: json!(
                DRAFT_KEYS
                    .iter()
                    .map(|key| ((*key).to_owned(), self.state[*key].clone()))
                    .collect::<serde_json::Map<String, Value>>()
            ),
            profile: self.profile.clone(),
            draft_id: self.draft_id.clone(),
            mode: self.mode.clone(),
            control: self.control.clone(),
            assistant: self.assistant.clone(),
        }
    }
    pub(super) fn restore_editor(&mut self, draft: EditorDraft) {
        self.bind(&draft.mode, &draft.control, &draft.assistant);
        for key in DRAFT_KEYS {
            self.state[*key] = draft.state[*key].clone();
        }
        self.profile = draft.profile;
        self.draft_id = draft.draft_id;
    }

    pub fn bind(&mut self, mode: &str, control: &str, assistant: &str) {
        let context = format!("{mode}/{control}/{assistant}");
        if self.context == context {
            return;
        }
        let sequence = self.sequence + 1;
        let mut cached = std::mem::take(&mut self.cached);
        let current = std::mem::take(self);
        if !current.context.is_empty() {
            cached.insert(current.context.clone(), Box::new(current));
        }
        if let Some(saved) = cached.remove(&context) {
            *self = *saved;
        } else {
            self.state = json!({"section":0,"ai_screen":"main","routing_owner":"main","revision":sequence,"secret_revision":sequence,"draft_revision":sequence,"profiles":[],"connections":[],"active_spec":{},"routing":{},"api_protocol":"openai_chat","provider_period":"none","provider_auto_resume":true});
        }
        self.cached = cached;
        self.context = context;
        self.mode = mode.into();
        self.control = control.into();
        self.assistant = assistant.into();
        self.sequence = sequence;
        self.state["secret_revision"] = json!(sequence);
        self.busy();
    }
    fn request(&mut self, purpose: &'static str, path: &str, body: Option<Value>) -> Request {
        self.sequence += 1;
        let request = Request {
            id: self.sequence,
            mode: self.mode.clone(),
            control: self.control.clone(),
            assistant: self.assistant.clone(),
            context: self.context.clone(),
            path: path.into(),
            body,
        };
        self.pending.insert(request.id, (purpose, request.clone()));
        self.busy();
        request
    }
    fn busy(&mut self) {
        self.state["ai_busy"] = json!(!self.pending.is_empty());
        self.state["routing_busy"] = json!(!self.pending.is_empty());
    }
    fn revision(&mut self) {
        self.sequence += 1;
        self.state["revision"] = json!(self.sequence);
    }
    pub fn load(&mut self) -> Vec<Request> {
        self.loaded = true;
        self.load_error = None;
        vec![
            self.request("agent", "/api/v1/agent", None),
            self.request("providers", "/api/v1/providers", None),
            self.request("routing", "/api/v1/agent/routing", None),
            self.request("allowances", "/api/v1/usage/providers", None),
        ]
    }
    pub fn invalidate(&mut self) {
        self.loaded = false;
    }
    pub fn ensure_loaded(&mut self) -> Vec<Request> {
        if self.loaded || !self.pending.is_empty() || self.context.is_empty() {
            vec![]
        } else {
            self.load()
        }
    }
    pub fn handles(action: &str) -> bool {
        action.starts_with("ai-")
            || action.starts_with("routing-")
            || action.starts_with("provider-")
            || matches!(
                action,
                "section"
                    | "history-section"
                    | "api-protocol"
                    | "profile-save"
                    | "profile-main"
                    | "task-profile"
            )
    }
    fn profile(&self, id: &str) -> Value {
        self.state["profiles"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|p| p["id"] == id)
            .cloned()
            .unwrap_or(Value::Null)
    }
    fn directory_provider(&self, id: &str) -> Value {
        self.state["directory"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|p| p["id"] == id)
            .cloned()
            .unwrap_or(Value::Null)
    }
    fn connection_defaults(&mut self) {
        let provider =
            self.directory_provider(self.state["connection_kind"].as_str().unwrap_or(""));
        let model = provider["models"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|m| m["id"] == self.state["connection_model"])
            .cloned()
            .unwrap_or(Value::Null);
        self.state["context_default"] = json!(
            model["context"]
                .as_u64()
                .filter(|n| *n > 0)
                .unwrap_or(32768)
        );
        let context = self.state["connection_context"]
            .as_str()
            .and_then(|s| s.parse::<u64>().ok())
            .unwrap_or(self.state["context_default"].as_u64().unwrap());
        self.state["output_default"] = json!(
            8192u64
                .min(
                    model["output"]
                        .as_u64()
                        .filter(|n| *n > 0)
                        .unwrap_or(u64::MAX)
                )
                .min(context.saturating_sub(1))
                .max(1)
        );
    }
    fn routing_profile(&mut self, id: &str) {
        let selected = self.profile(id);
        if selected.is_null() {
            return;
        }
        self.state["routing_profile"] = json!(id);
        self.state["routing_name"] = selected["name"].clone();
        self.state["routing_model"] = selected["model"].clone();
        let provider = selected["provider"].as_str().unwrap_or("");
        let allowance = self
            .allowances
            .iter()
            .find(|a| a["provider"] == provider)
            .cloned()
            .unwrap_or(json!({}));
        self.state["provider_period"] = allowance["policy"]["period"]
            .as_str()
            .unwrap_or("none")
            .into();
        self.state["provider_tokens"] = allowance["policy"]["limit"]
            .as_u64()
            .unwrap_or(2000000)
            .to_string()
            .into();
        self.state["provider_auto_resume"] = json!(allowance["policy"]["auto_resume"] != false);
        self.state["provider_allowance"] = allowance;
        self.state["routing_profile_limit"] = self.state["routing"]["profile_limits"][id]
            .as_u64()
            .map(|n| n.to_string())
            .unwrap_or_default()
            .into();
        self.state["routing_provider_limit"] = self.state["routing"]["provider_limits"][provider]
            .as_u64()
            .map(|n| n.to_string())
            .unwrap_or_default()
            .into();
        self.revision();
    }
    pub fn command(&mut self, v: &Value) -> Vec<Request> {
        let action = v["action"].as_str().unwrap_or("");
        if !self.pending.is_empty()
            && !matches!(
                action,
                "section" | "history-section" | "ai-open" | "task-profile"
            )
        {
            return vec![];
        }
        let mut requests = vec![];
        if action == "ai-retry" {
            if let Some((purpose, request)) = self.retry.take() {
                self.pending.insert(request.id, (purpose, request.clone()));
                self.state["retry"] = false.into();
                self.busy();
                return vec![request];
            }
            return vec![];
        }
        if self.retry.is_some()
            && matches!(
                action,
                "ai-save-connection"
                    | "ai-save-main"
                    | "routing-save"
                    | "profile-save"
                    | "profile-main"
                    | "provider-save"
            )
        {
            return vec![];
        }

        match action {
            "section" | "history-section" => {
                self.state["return_to_chat"] = false.into();
                self.state["section"] = v["value"].clone();
                self.state["routing_owner"] = "main".into();
                if v["value"] == 1 || v["value"] == 4 {
                    requests = self.ensure_loaded();
                }
            }
            "ai-open" => {
                self.state["section"] = json!(1);
                self.state["return_to_chat"] = json!(true);
                self.state["ai_screen"] = "main".into();
                requests = self.ensure_loaded();
            }
            "ai-screen" => self.state["ai_screen"] = v["screen"].clone(),
            "ai-more-providers" => {
                self.state["more_providers"] = json!(self.state["more_providers"] != true)
            }
            "ai-model-input" => {
                self.state["connection_model"] = v["model"].clone();
                self.connection_defaults();
            }
            "ai-context-input" => {
                self.state["connection_context"] = v["value"].clone();
                self.connection_defaults();
            }
            "ai-model" => {
                self.state["connection_model"] = v["model"].clone();
                let provider =
                    self.directory_provider(self.state["connection_kind"].as_str().unwrap_or(""));
                if let Some(model) = provider["models"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .find(|m| m["id"] == v["model"])
                {
                    self.state["api_endpoint"] = model["endpoint"].clone();
                    self.state["api_protocol"] = model["protocol"].clone();
                }
                self.connection_defaults();
            }
            "ai-back" => {
                self.state["ai_screen"] = "main".into();
                self.state["return_to_chat"] = false.into();
            }
            "ai-choose" => {
                let selected = self.profile(v["id"].as_str().unwrap_or(""));
                self.state["draft_profile"] = selected["id"].clone();
                self.state["draft_model"] = selected["model"].clone();
                self.state["draft_revision"] = json!(self.sequence + 1);
            }
            "ai-new" => {
                self.state["result"] = "".into();
                let kind = v["kind"].as_str().unwrap_or("custom");
                let provider = self.directory_provider(kind);
                let name = provider["name"]
                    .as_str()
                    .unwrap_or(super::i18n::text("ai.custom-api"));
                let endpoint = provider["endpoint"].as_str().unwrap_or("");
                let protocol = provider["protocol"].as_str().unwrap_or("openai_chat");
                self.draft_id = format!("agent-{}", uuid::Uuid::new_v4());
                self.state["connection_draft_id"] = self.draft_id.clone().into();
                self.profile = Value::Null;
                self.state["connection_profile_id"] = "".into();
                self.state["connection_kind"] = kind.into();
                self.state["connection_name"] = name.into();
                self.state["connection_model"] = "".into();
                self.state["connection_context"] = "".into();
                self.state["connection_output"] = "".into();
                self.state["api_endpoint"] = endpoint.into();
                self.state["api_protocol"] = protocol.into();
                self.state["has_api_key"] = false.into();
                self.state["connection_protocol_override"] = false.into();
                self.state["ai_screen"] = "manage".into();
                self.state["secret_revision"] = json!(self.sequence + 1);
                self.state["model_options"] = json!([]);
                self.state["check_message"] = "".into();
                self.connection_defaults();
            }
            "ai-manage" => {
                self.state["result"] = "".into();
                let id = v["id"].as_str().unwrap_or("");
                self.profile = self.profile(id);
                let connection = self.state["connections"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .find(|c| c["profile_id"] == id)
                    .cloned()
                    .unwrap_or(json!({}));
                self.state["connection_profile_id"] = json!(id);
                self.state["connection_name"] = self.profile["name"].clone();
                self.state["connection_model"] = self.profile["model"].clone();
                self.state["api_endpoint"] = connection["endpoint"].clone();
                self.state["api_protocol"] = connection["protocol"].clone();
                self.state["has_api_key"] = connection["has_key"].clone();
                self.state["connection_protocol_override"] = false.into();
                self.state["connection_kind"] = connection["model_settings"]["catalog_provider"]
                    .as_str()
                    .unwrap_or("custom")
                    .into();
                self.state["connection_context"] = connection["model_settings"]["context_window"]
                    .as_u64()
                    .map(|n| n.to_string())
                    .unwrap_or_default()
                    .into();
                self.state["connection_output"] = connection["model_settings"]["max_output_tokens"]
                    .as_u64()
                    .map(|n| n.to_string())
                    .unwrap_or_default()
                    .into();
                self.state["ai_screen"] = "manage".into();
                self.state["secret_revision"] = json!(self.sequence + 1);
                self.state["model_options"] = json!([]);
                self.state["check_message"] = "".into();
                self.connection_defaults();
            }
            "api-protocol" => {
                self.state["api_protocol"] = v["protocol"].clone();
                self.state["connection_protocol_override"] = json!(true);
            }
            "ai-api-check" => {
                requests.push(self.request("check","/api/v1/providers/check",Some(json!({"endpoint":endpoint(v["endpoint"].as_str().unwrap_or(""),self.state["api_protocol"].as_str().unwrap_or("openai_chat")),"api_key":v["key"],"protocol":self.state["api_protocol"],"profile_id":self.state["connection_profile_id"]}))));
            }
            "ai-save-connection" => {
                let mut target = self.state["connection_profile_id"]
                    .as_str()
                    .unwrap_or("")
                    .to_owned();
                if target.is_empty() {
                    target = self.draft_id.clone();
                }
                if v["name"].as_str().unwrap_or("").trim().is_empty()
                    || v["model"].as_str().unwrap_or("").trim().is_empty()
                {
                    self.state["result"] =
                        super::i18n::text("aiconfiguration.choose-or-enter-a-model").into();
                    return vec![];
                }
                let first = self.state["active_spec"]
                    .as_object()
                    .is_none_or(|s| s.is_empty());
                let protocol = self.state["api_protocol"].as_str().unwrap_or("openai_chat");
                let mut url = endpoint(v["endpoint"].as_str().unwrap_or(""), protocol);
                let mut protocol = protocol.to_owned();
                let directory =
                    self.directory_provider(self.state["connection_kind"].as_str().unwrap_or(""));
                if self.state["connection_protocol_override"] != true
                    && v["endpoint"] == self.state["api_endpoint"]
                    && let Some(model) = directory["models"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .find(|m| m["id"] == v["model"])
                {
                    url = model["endpoint"].as_str().unwrap_or(&url).to_owned();
                    protocol = model["protocol"].as_str().unwrap_or(&protocol).to_owned();
                }
                let parse = |key: &str| -> Result<Option<u64>, ()> {
                    let s = v[key].as_str().unwrap_or("").trim();
                    if s.is_empty() {
                        Ok(None)
                    } else {
                        s.parse::<u64>()
                            .ok()
                            .filter(|n| *n > 0 && *n <= 9_007_199_254_740_991)
                            .map(Some)
                            .ok_or(())
                    }
                };
                let (Ok(context), Ok(output)) = (parse("context"), parse("output")) else {
                    self.state["result"] = super::i18n::text("ai.invalid-token-limit").into();
                    return vec![];
                };
                self.after_save = Some(target.clone());
                if first {
                    self.activation = Some(target.clone());
                }
                requests.push(self.request("saved","/api/v1/providers",Some(json!({"profile_id":target,"name":v["name"],"model":v["model"],"endpoint":url,"api_key":v["key"],"protocol":protocol,"catalog_provider":self.state["connection_kind"].as_str().filter(|s|*s!="custom"),"context_window":context,"max_output_tokens":output,"expected_revision":self.profile["revision"].as_u64().unwrap_or(0),"request_id":uuid::Uuid::new_v4().to_string(),"follow_main":first}))));
            }
            "ai-save-main" | "profile-save" => {
                let id = self.state[if action == "ai-save-main" {
                    "draft_profile"
                } else {
                    "routing_profile"
                }]
                .as_str()
                .unwrap_or("");
                let mut selected = self.profile(id);
                if selected.is_null() {
                    self.state["result"] =
                        super::i18n::text("aiconfiguration.add-and-choose-an-agent-first").into();
                    return vec![];
                }
                let copy = v["copy"] == true;
                let expected = if copy {
                    0
                } else {
                    selected["revision"].as_u64().unwrap_or(0)
                };
                if copy {
                    selected["id"] = format!("profile-{}", uuid::Uuid::new_v4()).into();
                }
                selected["revision"] = json!(expected + 1);
                selected["model"] = v["model"].clone();
                if action == "profile-save" {
                    selected["name"] = v["name"].clone();
                }
                self.after_save = selected["id"].as_str().map(str::to_owned);
                if action == "ai-save-main" {
                    self.activation = self.after_save.clone();
                }
                requests.push(self.request("saved","/api/v1/agent",Some(json!({"profile":selected,"expected_revision":expected,"request_id":uuid::Uuid::new_v4().to_string(),"follow_main":action=="ai-save-main","follow_worker":false}))));
            }
            "task-profile" => {
                self.state["routing_owner"] = v["owner"].clone();
                self.state["routing_owner_name"] = v["title"].clone();
                self.state["section"] = json!(4);
                requests = self.ensure_loaded();
            }
            "routing-refresh" => requests = self.load(),
            "routing-edit" => self.routing_profile(v["id"].as_str().unwrap_or("")),
            "routing-choice" => {
                if v["kind"] == "worker" {
                    let id = v["id"].as_str().unwrap_or("");
                    self.state["routing"]["worker_profile"] = if id.is_empty() {
                        Value::Null
                    } else {
                        id.into()
                    };
                    self.state["routing"]["follow_chat"] = json!(id.is_empty());
                } else {
                    let id = v["id"].clone();
                    let mut list = self.state["routing"]["allowed_tool_profiles"]
                        .as_array()
                        .cloned()
                        .unwrap_or_default();
                    if list.contains(&id) {
                        list.retain(|a| a != &id);
                    } else {
                        list.push(id);
                    }
                    self.state["routing"]["allowed_tool_profiles"] = list.into();
                }
            }
            "routing-save" => {
                let mut policy = self.state["routing"].clone();
                let id = self.state["routing_profile"]
                    .as_str()
                    .unwrap_or("")
                    .to_owned();
                let selected = self.profile(&id);
                let provider = selected["provider"].as_str().unwrap_or("");
                for (key, target, input) in [
                    ("profile_limits", id.as_str(), "profile_limit"),
                    ("provider_limits", provider, "provider_limit"),
                ] {
                    let value = v[input].as_str().unwrap_or("").trim();
                    if policy[key].is_null() {
                        policy[key] = json!({});
                    }
                    if value.is_empty() {
                        policy[key].as_object_mut().unwrap().remove(target);
                    } else {
                        let Ok(n) = value.parse::<u64>() else {
                            self.state["result"] = super::i18n::text(
                                "settingspanel.concurrency-must-be-1-1000-leave-blank",
                            )
                            .into();
                            return vec![];
                        };
                        if !(1..=1000).contains(&n) {
                            self.state["result"] = super::i18n::text(
                                "settingspanel.concurrency-must-be-1-1000-leave-blank",
                            )
                            .into();
                            return vec![];
                        }
                        policy[key][target] = json!(n);
                    }
                }
                requests.push(self.request("routing-saved", "/api/v1/agent/routing", Some(policy)));
            }
            "profile-main" => {
                requests.push(self.request("saved","/api/v1/agent/config",Some(json!({"request_id":uuid::Uuid::new_v4().to_string(),"profile":self.state["routing_profile"],"owner":self.state["routing_owner"]}))));
            }
            "provider-period" => self.state["provider_period"] = v["period"].clone(),
            "provider-auto" => {
                self.state["provider_auto_resume"] =
                    json!(self.state["provider_auto_resume"] != true)
            }
            "provider-save" => {
                let Ok(limit) = v["tokens"].as_str().unwrap_or("").parse::<u64>() else {
                    return vec![];
                };
                if limit == 0 || limit > 9007199254740991 {
                    self.state["result"] = super::i18n::text(
                        "settingspanel.enter-a-valid-positive-integer-token-allowance",
                    )
                    .into();
                    return vec![];
                }
                let old = &self.state["provider_allowance"];
                let body = json!({"provider":old["provider"],"revision":old["revision"],"policy":{"period":self.state["provider_period"],"limit":limit,"timezone":old["policy"]["timezone"].as_str().unwrap_or("UTC"),"auto_resume":self.state["provider_auto_resume"]}});
                requests.push(self.request(
                    "allowance-saved",
                    "/api/v1/usage/providers",
                    Some(body),
                ));
            }
            _ => {}
        }
        self.revision();
        requests
    }
    pub fn reply(
        &mut self,
        id: u64,
        context: &str,
        body: Value,
        error: Option<&str>,
        rejected: bool,
    ) -> Vec<Request> {
        if context != self.context {
            if let Some(cached) = self.cached.get_mut(context) {
                cached.sequence = self.sequence;
                let requests = cached.reply(id, context, body, error, rejected);
                self.sequence = cached.sequence;
                return requests;
            }
            return vec![];
        }
        let Some((purpose, request)) = self.pending.remove(&id) else {
            return vec![];
        };
        self.busy();
        if let Some(error) = error {
            if matches!(purpose, "agent" | "providers" | "routing" | "allowances") {
                self.load_error = Some(error.into());
            }
            if request.body.is_some() && purpose != "check" && !rejected {
                self.retry = Some((purpose, request));
                self.state["retry"] = true.into();
            }
            self.state["result"] = error.into();
            self.revision();
            return vec![];
        }
        match purpose {
            "agent" => {
                self.state["profiles"] = body["profiles"].clone();
                self.state["active_spec"] = body["thread"]["config"]
                    .as_object()
                    .map(|s| json!(s))
                    .unwrap_or(json!({}));
                if self.state["draft_profile"].is_null() {
                    self.state["draft_profile"] =
                        self.state["active_spec"]["source_profile_id"].clone();
                    self.state["draft_model"] = self.state["active_spec"]["model"].clone();
                }
                let id = self.state["active_spec"]["source_profile_id"]
                    .as_str()
                    .unwrap_or("");
                self.state["active_name"] =
                    self.profile(id)["name"].as_str().unwrap_or("Pot").into();
                if self.state["routing_profile"].is_null() {
                    let id = self.state["profiles"]
                        .as_array()
                        .and_then(|p| p.first())
                        .and_then(|p| p["id"].as_str())
                        .unwrap_or("")
                        .to_owned();
                    self.routing_profile(&id);
                }
            }
            "providers" => {
                self.state["connections"] = body["connections"].clone();
                self.state["directory"] = body["directory"].clone();
                self.connection_defaults();
            }
            "routing" | "routing-saved" => self.state["routing"] = body,
            "allowances" => {
                self.allowances = body["providers"].as_array().cloned().unwrap_or_default();
                let id = self.state["routing_profile"]
                    .as_str()
                    .unwrap_or("")
                    .to_owned();
                self.routing_profile(&id);
            }
            "check" => {
                self.state["model_options"] = body["models"].clone();
                self.state["check_message"] = body["message"].clone();
            }
            "saved" => {
                self.state["editor_save_pending"] = json!(true);
                if request.path == "/api/v1/agent/config"
                    && body["pending"] != true
                    && self.state["routing_owner"] != "main"
                {
                    self.state["navigate_page"] = json!(3);
                }
                self.state["ai_screen"] = "main".into();
                self.state["secret_revision"] = json!(self.sequence + 1);
                self.revision();
                return self.load();
            }
            "allowance-saved" => {
                self.state["provider_allowance"] = body;
            }
            _ => {}
        }
        if self.pending.is_empty() {
            let id = self.state["routing_profile"]
                .as_str()
                .unwrap_or("")
                .to_owned();
            self.routing_profile(&id);
            if let Some(id) = self.after_save.take() {
                self.profile = self.profile(&id);
                self.state["connection_profile_id"] = id.clone().into();
                self.state["routing_profile"] = id.clone().into();
                self.state["draft_profile"] = id.into();
                self.state["draft_model"] = self.profile["model"].clone();
                self.state["draft_revision"] = json!(self.sequence + 1);
            }
            if self
                .activation
                .as_ref()
                .is_some_and(|id| self.state["active_spec"]["source_profile_id"] == *id)
            {
                self.activation = None;
                if self.state["return_to_chat"] == true {
                    self.state["navigate_page"] = json!(0);
                    self.state["return_to_chat"] = false.into();
                }
            }
            self.state["result"] = "".into();
        }
        if let Some(error) = &self.load_error {
            self.state["result"] = error.clone().into();
        }
        if self.pending.is_empty() && self.state["editor_save_pending"] == true {
            self.state["editor_save_pending"] = false.into();
            self.state["editor_saved"] = true.into();
        }
        self.revision();
        vec![]
    }
    pub fn projection(&self, host: &Value, local_execution_settings: bool) -> Value {
        let mut value = host.as_object().cloned().unwrap_or_default();
        for (key, item) in self.state.as_object().into_iter().flatten() {
            value.insert(key.clone(), item.clone());
        }
        if self.state["section"] == 0 && host["result"].as_str().is_some_and(|s| !s.is_empty()) {
            value.insert("result".into(), host["result"].clone());
        }
        if self.state["section"] == 4
            && local_execution_settings
            && host["result"].as_str().is_some_and(|s| !s.is_empty())
        {
            value.insert("result".into(), host["result"].clone());
        }
        value.insert(
            "revision".into(),
            json!(
                (self.state["revision"].as_u64().unwrap_or(0) << 32)
                    | host["revision"].as_u64().unwrap_or(0)
            ),
        );
        value.insert("selected_model".into(), self.profile["model"].clone());
        json!(value)
    }
}
pub fn endpoint(value: &str, protocol: &str) -> String {
    let value = value.trim().trim_end_matches('/');
    if value.ends_with("/v1")
        || value.ends_with("/v1beta")
        || value.split("://").nth(1).is_some_and(|s| !s.contains('/'))
    {
        let base = if value.split("://").nth(1).is_some_and(|s| !s.contains('/')) {
            format!(
                "{value}/{}",
                if protocol == "gemini" { "v1beta" } else { "v1" }
            )
        } else {
            value.into()
        };
        format!(
            "{base}{}",
            match protocol {
                "openai_responses" => "/responses",
                "anthropic_messages" => "/messages",
                "gemini" => "/models/{model}:streamGenerateContent",
                _ => "/chat/completions",
            }
        )
    } else {
        value.into()
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn editor_return_restores_the_parent_draft_without_rolling_back_live_data_or_requests() {
        let mut m = model();
        load(&mut m);
        m.command(&json!({"action":"ai-new","kind":"custom"}));
        m.state["connection_model"] = json!("parent-unsaved-model");
        m.state["more_providers"] = json!(true);
        let parent_id = m.draft_id.clone();
        let parent = m.capture_editor();
        m.command(&json!({"action":"ai-new","kind":"custom"}));
        m.state["profiles"] = json!([{"id":"fresh-profile","model":"fresh-model"}]);
        let request = m.request("check", "/api/v1/providers/check", None);
        let revision = m.state["revision"].clone();
        m.restore_editor(parent);
        assert_eq!(m.state["connection_model"], "parent-unsaved-model");
        assert_eq!(m.state["more_providers"], true);
        assert_eq!(m.draft_id, parent_id);
        assert_eq!(m.state["profiles"][0]["id"], "fresh-profile");
        assert_eq!(m.state["revision"], revision);
        assert!(m.pending.contains_key(&request.id));
        m.reply(
            request.id,
            &request.context,
            json!({"models":["discovered"]}),
            None,
            false,
        );
        assert_eq!(m.state["model_options"], json!(["discovered"]));
    }
    #[test]
    fn editor_return_restores_its_original_assistant_without_overwriting_another_context() {
        let mut m = model();
        load(&mut m);
        m.state["connection_model"] = json!("original-draft");
        let source = m.capture_editor();
        let source_context = m.context.clone();
        m.bind("local", "control-a", "other");
        m.state["connection_model"] = json!("other-draft");
        let other_context = m.context.clone();
        m.restore_editor(source);
        assert_eq!(m.context, source_context);
        assert_eq!(m.state["connection_model"], "original-draft");
        assert_eq!(
            m.cached[&other_context].state["connection_model"],
            "other-draft"
        );
    }
    fn model() -> SettingsState {
        let mut model = SettingsState::default();
        model.bind("local", "control-a", "main");
        model
    }
    fn load(model: &mut SettingsState) {
        let requests = model.load();
        for request in requests.into_iter().rev() {
            let body = match request.path.as_str() {
                "/api/v1/agent" => {
                    json!({"profiles":[{"id":"p","provider":"api","revision":3,"name":"Agent","model":"model"}],"thread":{"config":{"source_profile_id":"p","model":"model"}}})
                }
                "/api/v1/providers" => json!({"connections":[]}),
                "/api/v1/agent/routing" => {
                    json!({"revision":2,"follow_chat":true,"profile_limits":{"p":4},"provider_limits":{"api":5}})
                }
                _ => {
                    json!({"providers":[{"provider":"api","revision":2,"policy":{"period":"week","limit":1234,"auto_resume":false}}]})
                }
            };
            model.reply(request.id, &request.context, body, None, false);
        }
    }
    #[test]
    fn changed_control_or_assistant_fences_late_configuration_and_secrets() {
        let mut m = model();
        let requests = m.load();
        m.bind("local", "control-b", "main");
        for request in requests {
            assert!(
                m.reply(
                    request.id,
                    &request.context,
                    json!({"profiles":[{"id":"stale"}]}),
                    None,
                    false
                )
                .is_empty()
            );
        }
        assert_eq!(m.state["profiles"], json!([]));
        m.bind("local", "control-b", "other");
        assert_ne!(m.context, "local/control-b/main");
    }
    #[test]
    fn unordered_load_combines_routing_and_allowance_without_overwriting_edited_model() {
        let mut m = model();
        load(&mut m);
        assert_eq!(m.state["routing_profile_limit"], "4");
        assert_eq!(m.state["routing_provider_limit"], "5");
        assert_eq!(m.state["provider_tokens"], "1234");
        assert_eq!(m.state["provider_auto_resume"], false);
        m.command(&json!({"action":"ai-choose","id":"p"}));
        let revision = m.state["draft_revision"].clone();
        load(&mut m);
        assert_eq!(m.state["draft_profile"], "p");
        assert_eq!(m.state["draft_revision"], revision);
    }
    #[test]
    fn uncertain_save_retries_exact_request_but_confirmed_rejection_allows_correction() {
        let mut m = model();
        load(&mut m);
        m.command(&json!({"action":"ai-new","kind":"custom"}));
        let v = json!({"action":"ai-save-connection","name":"Test","model":"model","endpoint":"https://example.invalid/v1","key":"fixture-key"});
        let request = m.command(&v).remove(0);
        m.reply(
            request.id,
            &request.context,
            Value::Null,
            Some("connection lost"),
            false,
        );
        assert!(m.command(&v).is_empty());
        let retry = m.command(&json!({"action":"ai-retry"})).remove(0);
        assert_eq!(retry.body, request.body);
        assert_eq!(retry.context, request.context);
        assert!(!m.state.to_string().contains("fixture-key"));
        m.reply(
            retry.id,
            &retry.context,
            Value::Null,
            Some("invalid model"),
            true,
        );
        assert!(!m.command(&v).is_empty());
    }
    #[test]
    fn switched_assistant_retains_in_flight_save_and_exact_retry_without_crossing_contexts() {
        let mut m = model();
        load(&mut m);
        m.command(&json!({"action":"ai-new","kind":"custom"}));
        let save=m.command(&json!({"action":"ai-save-connection","name":"Draft","model":"model","endpoint":"https://example.invalid/v1","key":"transient-secret"})).remove(0);
        m.bind("local", "control-a", "other");
        let current = m.state.clone();
        m.reply(
            save.id,
            &save.context,
            Value::Null,
            Some("lost response"),
            false,
        );
        assert_eq!(m.state, current);
        m.bind("local", "control-a", "main");
        let retry = m.command(&json!({"action":"ai-retry"})).remove(0);
        assert_eq!(retry.id, save.id);
        assert_eq!(retry.body, save.body);
        assert_eq!(retry.assistant, "main");
        assert!(!m.state.to_string().contains("transient-secret"));
    }
    #[test]
    fn check_and_routing_changes_never_submit_a_model_message() {
        let mut m = model();
        load(&mut m);
        let req=m.command(&json!({"action":"ai-api-check","endpoint":"https://example.invalid/v1","key":"fixture"})).remove(0);
        assert_eq!(req.path, "/api/v1/providers/check");
        m.reply(
            req.id,
            &req.context,
            json!({"models":["model"],"message":"Ready"}),
            None,
            false,
        );
        m.command(&json!({"action":"routing-choice","kind":"worker","id":"p"}));
        let req = m
            .command(&json!({"action":"routing-save","profile_limit":"7","provider_limit":""}))
            .remove(0);
        assert_eq!(req.path, "/api/v1/agent/routing");
        assert_eq!(req.body.as_ref().unwrap()["profile_limits"]["p"], 7);
        assert_eq!(req.body.as_ref().unwrap()["follow_chat"], false);
        assert_eq!(req.body.as_ref().unwrap()["worker_profile"], "p");
        assert!(
            req.body.as_ref().unwrap()["provider_limits"]
                .get("api")
                .is_none()
        );
    }
    #[test]
    fn provider_routes_preserve_protocol_specific_endpoints() {
        assert_eq!(
            endpoint("https://example.invalid/v1", "openai_responses"),
            "https://example.invalid/v1/responses"
        );
        assert_eq!(
            endpoint("https://example.invalid", "gemini"),
            "https://example.invalid/v1beta/models/{model}:streamGenerateContent"
        );
    }
    fn directory(m: &mut SettingsState) {
        m.state["directory"] = json!([
            {"id":"first","name":"First","common":true,"endpoint":"https://first.invalid/v1/chat/completions","protocol":"openai_chat","models":[{"id":"chat","name":"Chat","context":64000,"output":4000,"endpoint":"https://first.invalid/claude/v1/messages","protocol":"anthropic_messages"}]},
            {"id":"other","name":"Another Provider","common":false,"models":[]}
        ]);
    }
    #[test]
    fn collapsed_providers_are_always_searchable_and_more_can_be_collapsed() {
        let mut m = model();
        directory(&mut m);
        assert_eq!(provider_choices(&m.state, "").len(), 1);
        assert_eq!(provider_choices(&m.state, " PROVIDER ")[0]["id"], "other");
        assert_eq!(provider_choices(&m.state, "OTHER").len(), 1);
        m.command(&json!({"action":"ai-more-providers"}));
        assert_eq!(provider_choices(&m.state, "").len(), 2);
        m.command(&json!({"action":"ai-more-providers"}));
        assert_eq!(provider_choices(&m.state, "").len(), 1);
    }
    #[test]
    fn empty_limits_remain_inherited_and_model_changes_preserve_overrides() {
        let mut m = model();
        load(&mut m);
        directory(&mut m);
        m.command(&json!({"action":"ai-new","kind":"first"}));
        assert_eq!(m.state["connection_name"], "First");
        assert_eq!(m.state["connection_context"], "");
        m.command(&json!({"action":"ai-model","model":"chat"}));
        assert_eq!(m.state["context_default"], 64000);
        assert_eq!(m.state["output_default"], 4000);
        assert_eq!(m.state["api_protocol"], "anthropic_messages");
        assert_eq!(
            m.state["api_endpoint"],
            "https://first.invalid/claude/v1/messages"
        );
        m.command(&json!({"action":"ai-context-input","value":"16000"}));
        m.command(&json!({"action":"ai-model-input","model":"unlisted"}));
        assert_eq!(m.state["connection_context"], "16000");
        assert_eq!(m.state["context_default"], 32768);
        let save = json!({"action":"ai-save-connection","name":"First","model":"unlisted","endpoint":m.state["api_endpoint"],"key":"fixture","context":"","output":""});
        let request = m.command(&save).remove(0);
        let body = request.body.unwrap();
        assert_eq!(body["context_window"], Value::Null);
        assert_eq!(body["max_output_tokens"], Value::Null);
        assert_eq!(body["catalog_provider"], "first");
        assert_eq!(m.state["connection_context"], "16000");
    }
    #[test]
    fn explicit_limits_and_saved_empty_overrides_round_trip_without_secret_state() {
        let mut m = model();
        load(&mut m);
        directory(&mut m);
        m.state["connections"] = json!([{"profile_id":"p","endpoint":"https://first.invalid/v1/chat/completions","protocol":"openai_chat","has_key":true,"model_settings":{"catalog_provider":"first","context_window":16000,"max_output_tokens":1000}}]);
        m.command(&json!({"action":"ai-manage","id":"p"}));
        assert_eq!(m.state["connection_context"], "16000");
        assert_eq!(m.state["connection_output"], "1000");
        let mut save = json!({"action":"ai-save-connection","name":"First","model":"chat","endpoint":m.state["api_endpoint"],"key":"transient-secret","context":"16000","output":"1000"});
        let request = m.command(&save).remove(0);
        assert_eq!(request.body.as_ref().unwrap()["context_window"], 16000);
        assert_eq!(request.body.as_ref().unwrap()["max_output_tokens"], 1000);
        assert_eq!(
            request.body.as_ref().unwrap()["protocol"],
            "anthropic_messages"
        );
        assert!(!m.state.to_string().contains("transient-secret"));
        m.reply(
            request.id,
            &request.context,
            Value::Null,
            Some("fixture rejected"),
            true,
        );
        for invalid in ["0", "-1", "3.5", "oops", "9007199254740992"] {
            save["context"] = json!(invalid);
            assert!(m.command(&save).is_empty());
        }
    }
}
