//! Configuration state and request ownership shared by both hosts.
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::collections::BTreeMap;
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
        if !self.pending.is_empty() && !matches!(action, "section" | "history-section") {
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
                let kind = v["kind"].as_str().unwrap_or("custom");
                let (name, endpoint, protocol) = match kind {
                    "anthropic" => (
                        "Claude",
                        "https://api.anthropic.com/v1/messages",
                        "anthropic_messages",
                    ),
                    "gemini" => (
                        "Gemini",
                        "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent",
                        "gemini",
                    ),
                    "deepseek" => (
                        "DeepSeek",
                        "https://api.deepseek.com/v1/chat/completions",
                        "openai_chat",
                    ),
                    "opencode-go" => (
                        "OpenCode Go",
                        "https://opencode.ai/zen/go/v1/chat/completions",
                        "openai_chat",
                    ),
                    "opencode-zen" => (
                        "OpenCode Zen",
                        "https://opencode.ai/zen/v1/chat/completions",
                        "openai_chat",
                    ),
                    "openai" => ("OpenAI API", "https://api.openai.com/v1", "openai_chat"),
                    _ => (super::i18n::text("ai.custom-api"), "", "openai_chat"),
                };
                self.draft_id = format!("agent-{}", uuid::Uuid::new_v4());
                self.state["connection_draft_id"] = self.draft_id.clone().into();
                self.profile = Value::Null;
                self.state["connection_profile_id"] = "".into();
                self.state["connection_kind"] = kind.into();
                self.state["connection_name"] = name.into();
                self.state["connection_model"] = "".into();
                self.state["api_endpoint"] = endpoint.into();
                self.state["api_protocol"] = protocol.into();
                self.state["has_api_key"] = false.into();
                self.state["ai_screen"] = "manage".into();
                self.state["secret_revision"] = json!(self.sequence + 1);
                self.state["model_options"] = json!([]);
                self.state["check_message"] = "".into();
            }
            "ai-manage" => {
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
                self.state["connection_kind"] =
                    brand(connection["endpoint"].as_str().unwrap_or("")).into();
                self.state["ai_screen"] = "manage".into();
                self.state["secret_revision"] = json!(self.sequence + 1);
                self.state["model_options"] = json!([]);
                self.state["check_message"] = "".into();
            }
            "api-protocol" => self.state["api_protocol"] = v["protocol"].clone(),
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
                if self.state["connection_kind"]
                    .as_str()
                    .unwrap_or("")
                    .starts_with("opencode-")
                {
                    let Some((u, p)) = open_code(
                        self.state["connection_kind"].as_str().unwrap(),
                        v["model"].as_str().unwrap_or(""),
                    ) else {
                        self.state["result"] = super::i18n::text(
                            "aiconfiguration.this-opencode-model-has-no-verified-request",
                        )
                        .into();
                        return vec![];
                    };
                    url = u;
                    protocol = p.into();
                }
                self.after_save = Some(target.clone());
                if first {
                    self.activation = Some(target.clone());
                }
                requests.push(self.request("saved","/api/v1/providers",Some(json!({"profile_id":target,"name":v["name"],"model":v["model"],"endpoint":url,"api_key":v["key"],"protocol":protocol,"expected_revision":self.profile["revision"].as_u64().unwrap_or(0),"request_id":uuid::Uuid::new_v4().to_string(),"follow_main":first}))));
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
            "providers" => self.state["connections"] = body["connections"].clone(),
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
            self.state["result"] = super::i18n::text(
                "settingspanel.configuration-loaded-existing-tasks-keep-their-original",
            )
            .into();
        }
        if let Some(error) = &self.load_error {
            self.state["result"] = error.clone().into();
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
fn brand(endpoint: &str) -> &'static str {
    if endpoint.contains("api.anthropic.com") {
        "anthropic"
    } else if endpoint.contains("generativelanguage.googleapis.com") {
        "gemini"
    } else if endpoint.contains("api.openai.com") {
        "openai"
    } else if endpoint.contains("api.deepseek.com") {
        "deepseek"
    } else if endpoint.contains("opencode.ai/zen/go/") {
        "opencode-go"
    } else if endpoint.contains("opencode.ai/zen/") {
        "opencode-zen"
    } else {
        "custom"
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
fn open_code(kind: &str, model: &str) -> Option<(String, &'static str)> {
    let routes: Value = serde_json::from_str(include_str!("open-code-routes.json")).ok()?;
    let suffix = routes[kind][model].as_str()?;
    let protocol = if suffix == "responses" {
        "openai_responses"
    } else if suffix == "messages" {
        "anthropic_messages"
    } else if suffix.starts_with("models/") {
        "gemini"
    } else {
        "openai_chat"
    };
    Some((
        format!(
            "https://opencode.ai/zen/{}/v1/{suffix}",
            if kind == "opencode-go" { "go" } else { "" }
        )
        .replace("zen//", "zen/"),
        protocol,
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
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
        assert_eq!(
            open_code("opencode-go", "gpt-6-luna").unwrap().1,
            "openai_responses"
        );
        assert!(open_code("opencode-go", "unknown").is_none());
        for (kind, model, suffix, protocol) in [
            (
                "opencode-go",
                "minimax-m3",
                "/go/v1/messages",
                "anthropic_messages",
            ),
            (
                "opencode-zen",
                "minimax-m3",
                "/zen/v1/chat/completions",
                "openai_chat",
            ),
            (
                "opencode-go",
                "qwen3.8-max",
                "/go/v1/messages",
                "anthropic_messages",
            ),
            (
                "opencode-zen",
                "qwen3.8-max",
                "/zen/v1/chat/completions",
                "openai_chat",
            ),
            (
                "opencode-zen",
                "gemini-3.1-pro",
                "/zen/v1/models/{model}:streamGenerateContent",
                "gemini",
            ),
        ] {
            let (endpoint, actual_protocol) = open_code(kind, model).unwrap();
            assert!(endpoint.ends_with(suffix), "{kind}/{model}: {endpoint}");
            assert_eq!(actual_protocol, protocol);
        }
        assert!(open_code("opencode-go", "claude-opus-4-6").is_none());
        assert!(open_code("opencode-zen", "gpt-future-unknown").is_none());
        assert!(open_code("custom", "minimax-m3").is_none());
    }
}
