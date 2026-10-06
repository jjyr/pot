//! Single-model live evaluation. Pi is a read-only configuration source, not a runtime dependency.
use crate::{Report, Scenario, runner::run_driver_configured};
use anyhow::{Result, ensure};
use futures::{FutureExt, StreamExt, future::BoxFuture};
use pot_core::{ExecutionServices, ModelRequest};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{
    fs::{self, File, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct LiveLimits {
    pub max_requests: u64,
    /// Conservatively reserves encoded request bytes + output cap for every attempt.
    pub max_reserved_tokens: u64,
}
impl Default for LiveLimits {
    fn default() -> Self {
        Self {
            max_requests: 8,
            max_reserved_tokens: 100_000,
        }
    }
}
/// Deliberately not Debug/Serialize: the credential must never enter records.
#[derive(Clone)]
pub struct DeepSeekConnection {
    endpoint: String,
    key: String,
    model: String,
    context_window: u64,
    max_output: u64,
    compat: Value,
    cost: Value,
}
impl DeepSeekConnection {
    pub fn from_pi(path: &Path) -> Result<Self> {
        let config: Value = serde_json::from_slice(&fs::read(path)?)?;
        let provider = &config["providers"]["deepseek"];
        ensure!(
            provider["api"].as_str() == Some("openai-completions"),
            "DeepSeek requires the Pi openai-completions configuration"
        );
        ensure!(
            provider["headers"].as_object().is_none_or(|h| h.is_empty()),
            "Custom Pi headers are not supported by this integration"
        );
        let models = provider["models"]
            .as_array()
            .ok_or_else(|| anyhow::anyhow!("Pi DeepSeek models missing"))?;
        let flash = models
            .iter()
            .filter(|m| {
                m["id"]
                    .as_str()
                    .is_some_and(|id| id == "deepseek-v4-flash" || id == "deepseek-flash")
            })
            .collect::<Vec<_>>();
        ensure!(
            flash.len() == 1,
            "Expected exactly one configured DeepSeek Flash model"
        );
        let model = flash[0];
        ensure!(
            model["api"]
                .as_str()
                .is_none_or(|api| api == "openai-completions"),
            "Flash protocol override is unsupported"
        );
        let base = provider["baseUrl"]
            .as_str()
            .ok_or_else(|| anyhow::anyhow!("Pi DeepSeek base URL missing"))?;
        let url =
            reqwest::Url::parse(base).map_err(|_| anyhow::anyhow!("Invalid DeepSeek endpoint"))?;
        ensure!(
            url.scheme() == "https"
                && url.username().is_empty()
                && url.password().is_none()
                && url.query().is_none()
                && url.fragment().is_none(),
            "DeepSeek endpoint must be credential-free HTTPS"
        );
        let configured = provider["apiKey"]
            .as_str()
            .ok_or_else(|| anyhow::anyhow!("Pi DeepSeek API key missing"))?;
        ensure!(
            !configured.starts_with('!'),
            "Command-based Pi secrets are unsupported; supply an environment variable reference"
        );
        let key = std::env::var(configured).unwrap_or_else(|_| configured.to_owned());
        ensure!(!key.is_empty(), "DeepSeek API key is empty");
        Ok(Self {
            endpoint: format!("{}/chat/completions", base.trim_end_matches('/')),
            key,
            model: model["id"].as_str().unwrap().into(),
            context_window: model["contextWindow"]
                .as_u64()
                .ok_or_else(|| anyhow::anyhow!("Pi Flash context window missing"))?,
            max_output: model["maxTokens"]
                .as_u64()
                .ok_or_else(|| anyhow::anyhow!("Pi Flash output limit missing"))?,
            compat: model["compat"].clone(),
            cost: model["cost"].clone(),
        })
    }
    pub fn model(&self) -> &str {
        &self.model
    }
    fn configure(&self, scenario: &mut Scenario) -> Result<Value> {
        ensure!(
            scenario.profile.engine == pot_core::Engine::Pot,
            "DeepSeek live evaluation requires Pot Agent"
        );
        ensure!(
            scenario.responses.is_empty(),
            "Live scenarios must not include scripted model responses"
        );
        ensure!(
            scenario.profile.params.max_output_tokens <= self.max_output,
            "Scenario output cap exceeds Pi Flash limit"
        );
        ensure!(
            scenario.profile.params.agent.context_window <= self.context_window,
            "Scenario context window exceeds Pi Flash limit"
        );
        scenario.profile.provider = "deepseek".into();
        scenario.profile.auth_ref = "providers.json:deepseek".into();
        scenario.profile.model = self.model.clone();
        scenario.protocol = "openai_chat".into();
        let effort = scenario.profile.params.reasoning_effort.as_deref();
        let enabled = effort.is_some_and(|effort| effort != "none");
        let effective = if enabled {
            let mapped = self.compat["reasoningEffortMap"][effort.unwrap()]
                .as_str()
                .unwrap_or(effort.unwrap());
            ensure!(
                matches!(mapped, "high" | "max"),
                "Unsupported DeepSeek effective reasoning effort"
            );
            Some(mapped.to_owned())
        } else {
            None
        };
        Ok(
            json!({"provider":"deepseek","model":self.model,"endpoint":self.endpoint,"thinking":if enabled {"enabled"} else {"disabled"},"reasoning_effort":effective,"pi_model_context_window":self.context_window,"pi_model_max_output":self.max_output,"compat":self.compat,"pi_configured_price_per_million":self.cost,"price_verified":false}),
        )
    }
}
struct LiveState {
    requests: u64,
    reserved: u64,
    records: Option<File>,
}
/// Shared across a sweep or an Agent tree. Reservations are not observed usage.
pub struct LiveBudget {
    limits: LiveLimits,
    deadline: std::time::Instant,
    consumed: Mutex<(u64, u64)>,
}
impl LiveBudget {
    pub fn new(limits: LiveLimits, deadline_ms: u64) -> Result<Self> {
        ensure!(
            limits.max_requests > 0 && limits.max_reserved_tokens > 0 && deadline_ms > 0,
            "Invalid shared budget"
        );
        Ok(Self {
            limits,
            deadline: std::time::Instant::now() + std::time::Duration::from_millis(deadline_ms),
            consumed: Mutex::new((0, 0)),
        })
    }
    pub fn reserve(&self, bound: u64) -> Result<()> {
        let mut consumed = self.consumed.lock().unwrap();
        ensure!(
            std::time::Instant::now() < self.deadline,
            "Shared deadline exceeded"
        );
        ensure!(
            consumed.0 < self.limits.max_requests,
            "Shared request limit reached"
        );
        let reserved = consumed
            .1
            .checked_add(bound)
            .ok_or_else(|| anyhow::anyhow!("Shared reservation overflow"))?;
        ensure!(
            reserved <= self.limits.max_reserved_tokens,
            "Shared reservation limit reached"
        );
        consumed.0 += 1;
        consumed.1 = reserved;
        Ok(())
    }
    pub fn snapshot(&self) -> Value {
        let consumed = self.consumed.lock().unwrap();
        json!({"attempts":consumed.0,"reserved_units":consumed.1,"limits":self.limits})
    }
    fn remaining(&self) -> std::time::Duration {
        self.deadline
            .saturating_duration_since(std::time::Instant::now())
    }
}
struct DeepSeekServices {
    connection: DeepSeekConnection,
    limits: LiveLimits,
    config: Value,
    state: Mutex<LiveState>,
    client: reqwest::Client,
    raw: Arc<Mutex<Option<File>>>,
    directory: PathBuf,
    budgets: Vec<Arc<LiveBudget>>,
    request_timeout: std::time::Duration,
}
impl ExecutionServices for DeepSeekServices {
    fn model(
        &self,
        mut request: ModelRequest,
    ) -> BoxFuture<'_, Result<futures::stream::BoxStream<'_, Result<Vec<u8>>>>> {
        async move {
            ensure!(request.protocol == "openai_chat" && request.provider == "deepseek" && request.model == self.connection.model, "Live request escaped the selected DeepSeek Flash connection");
            request.body["thinking"] = json!({"type":self.config["thinking"]});
            request.body.as_object_mut().unwrap().remove("reasoning_effort");
            if let Some(effort) = self.config["reasoning_effort"].as_str() {request.body["reasoning_effort"] = json!(effort);}
            if self.connection.compat["requiresReasoningContentOnAssistantMessages"] == true
                && let Some(messages) = request.body["messages"].as_array_mut() {
                    for message in messages.iter_mut().filter(|m| m["role"] == "assistant") {
                        message.as_object_mut().unwrap().entry("reasoning_content").or_insert(json!(""));
                    }
                }
            let bytes = serde_json::to_vec(&request.body)?;
            let output = request.body["max_tokens"].as_u64().ok_or_else(|| anyhow::anyhow!("Output bound missing"))?;
            let bound = (bytes.len() as u64).checked_add(output).ok_or_else(|| anyhow::anyhow!("Request reservation overflow"))?;
            {
                let mut state = self.state.lock().unwrap();
                ensure!(state.requests < self.limits.max_requests, "Live request count limit reached");
                let reserved = state.reserved.checked_add(bound).ok_or_else(|| anyhow::anyhow!("Run reservation overflow"))?;
                ensure!(reserved <= self.limits.max_reserved_tokens, "Live payload/output reservation limit reached");
                for budget in &self.budgets { budget.reserve(bound)?; }
                state.requests += 1; state.reserved = reserved;
                if state.records.is_none() {state.records = Some(private_file(&self.directory.join("requests.jsonl"))?);}
                let records = state.records.as_mut().unwrap();
                serde_json::to_writer(&mut *records,&json!({"id":request.id,"stage":if request.compaction_id.is_some(){"compaction"}else{"agent"},"compaction_id":request.compaction_id,"body":request.body,"reserved_tokens":bound}))?;
                records.write_all(b"\n")?; records.flush()?;
            }
            let timeout = self.budgets.iter().map(|b|b.remaining()).fold(self.request_timeout,std::cmp::min);
            let response = self.client.post(&self.connection.endpoint).timeout(timeout).bearer_auth(&self.connection.key).body(bytes).header("content-type","application/json").send().await.map_err(|_| anyhow::anyhow!("DeepSeek connection failed"))?;
            ensure!(response.status().is_success(), "DeepSeek HTTP status {}",response.status().as_u16());
            {let mut raw = self.raw.lock().unwrap(); if raw.is_none() {*raw = Some(private_file(&self.directory.join("responses.jsonl"))?);}}
            let raw = self.raw.clone(); let id = request.id;
            let stream = response.bytes_stream().map(move |chunk| {
                let chunk = chunk.map_err(|_| anyhow::anyhow!("DeepSeek response stream failed"))?;
                let mut raw_file = raw.lock().unwrap();
                let file = raw_file.as_mut().unwrap();
                // Exact response chunk bytes are retained for independent usage replay.
                serde_json::to_writer(&mut *file,&json!({"request_id":id,"bytes":chunk.to_vec()}))?;
                file.write_all(b"\n")?; file.flush()?;
                Ok(chunk.to_vec())
            }).boxed();
            Ok(stream)
        }.boxed()
    }
    fn rpc<'a>(
        &'a self,
        _session: Option<&'a str>,
        _id: &'a str,
        _method: &'a str,
        _params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        async { anyhow::bail!("External tools are not configured for live evaluation") }.boxed()
    }
    fn compaction(&self, _event: Value) -> BoxFuture<'_, Result<()>> {
        async { Ok(()) }.boxed()
    }
}
fn private_file(path: &Path) -> Result<File> {
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    Ok(options.open(path)?)
}
pub async fn run_deepseek(
    scenario: &Scenario,
    directory: &Path,
    connection: DeepSeekConnection,
    limits: LiveLimits,
) -> Result<Report> {
    let (scenario, service, config) =
        deepseek_services(scenario, directory, connection, limits, vec![])?;
    let host = pot_agent::AgentHost::with_services(service);
    run_driver_configured(
        &host,
        &scenario,
        directory,
        "native_policy_live",
        Some(config),
    )
    .await
}
pub(crate) fn deepseek_services(
    scenario: &Scenario,
    directory: &Path,
    connection: DeepSeekConnection,
    limits: LiveLimits,
    budgets: Vec<Arc<LiveBudget>>,
) -> Result<(Scenario, Arc<dyn ExecutionServices>, Value)> {
    ensure!(
        limits.max_requests > 0 && limits.max_reserved_tokens > 0,
        "Live limits must be positive"
    );
    let mut scenario = scenario.clone();
    let mut config = connection.configure(&mut scenario)?;
    config["limits"] = serde_json::to_value(&limits)?;
    let service = Arc::new(DeepSeekServices {
        connection,
        limits,
        config: config.clone(),
        state: Mutex::new(LiveState {
            requests: 0,
            reserved: 0,
            records: None,
        }),
        client: reqwest::Client::builder()
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .connect_timeout(std::time::Duration::from_secs(10))
            .timeout(std::time::Duration::from_millis(
                scenario.profile.params.agent.request_timeout_ms,
            ))
            .build()?,
        raw: Arc::new(Mutex::new(None)),
        directory: directory.to_path_buf(),
        budgets,
        request_timeout: std::time::Duration::from_millis(
            scenario.profile.params.agent.request_timeout_ms,
        ),
    });
    Ok((scenario, service, config))
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        Json, Router,
        http::HeaderMap,
        response::sse::{Event, Sse},
        routing::post,
    };
    use std::{
        collections::BTreeMap,
        convert::Infallible,
        sync::atomic::{AtomicUsize, Ordering},
    };
    struct Temp(PathBuf);
    impl Temp {
        fn new() -> Self {
            let path =
                std::env::temp_dir().join(format!("pot-deepseek-eval-{}", uuid::Uuid::new_v4()));
            fs::create_dir(&path).unwrap();
            Self(path)
        }
    }
    impl Drop for Temp {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }
    struct Config;
    impl pot_core::ProfileConfig for Config {
        fn model(&self) -> &str {
            "fixture"
        }
        fn max_tokens(&self) -> u64 {
            128
        }
    }
    fn scenario() -> Scenario {
        let mut profile = pot_core::Profile::api(&Config);
        profile.params.agent.builtin_tools.clear();
        Scenario {
            id: "live-contract-v1".into(),
            profile,
            protocol: "openai_chat".into(),
            turns: vec!["Say done".into()],
            responses: vec![],
            fixtures: BTreeMap::new(),
            expected_files: BTreeMap::new(),
            expected_final_text: Some("done".into()),
            turn_assertions: vec![],
            deadline_ms: 5000,
        }
    }
    fn connection(endpoint: String) -> DeepSeekConnection {
        DeepSeekConnection {
            endpoint,
            key: "credential-sentinel".into(),
            model: "deepseek-v4-flash".into(),
            context_window: 1_000_000,
            max_output: 384_000,
            compat: json!({"requiresReasoningContentOnAssistantMessages":true,"reasoningEffortMap":{"low":"high","high":"max"}}),
            cost: json!({"input":0.14,"output":0.28}),
        }
    }
    async fn mock() -> (String, Arc<AtomicUsize>, tokio::task::JoinHandle<()>) {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let count = Arc::new(AtomicUsize::new(0));
        let called = count.clone();
        let handler = move |headers: HeaderMap, Json(body): Json<Value>| {
            called.fetch_add(1, Ordering::SeqCst);
            async move {
                assert_eq!(headers["authorization"], "Bearer credential-sentinel");
                assert_eq!(body["model"], "deepseek-v4-flash");
                assert_eq!(body["thinking"]["type"], "disabled");
                assert!(body.get("reasoning_effort").is_none());
                let frames = [
                    json!({"choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}),
                    json!({"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":10,"prompt_cache_hit_tokens":60}}),
                ];
                Sse::new(futures::stream::iter(frames.into_iter().map(|frame| {
                    Ok::<_, Infallible>(Event::default().data(frame.to_string()))
                })))
            }
        };
        let router = Router::new().route("/chat/completions", post(handler));
        let server = tokio::spawn(async move {
            axum::serve(listener, router).await.unwrap();
        });
        (format!("http://{address}/chat/completions"), count, server)
    }
    #[test]
    fn pi_loader_selects_only_flash_and_excludes_secret_from_metadata() {
        let temp = Temp::new();
        let path = temp.0.join("models.json");
        let config = json!({"providers":{"deepseek":{"api":"openai-completions","baseUrl":"https://api.deepseek.com","apiKey":"credential-sentinel","models":[{"id":"deepseek-v4-pro"},{"id":"deepseek-v4-flash","contextWindow":1_000_000,"maxTokens":384_000}]}}});
        fs::write(&path, config.to_string()).unwrap();
        let connection = DeepSeekConnection::from_pi(&path).unwrap();
        assert_eq!(connection.model(), "deepseek-v4-flash");
        assert!(
            !connection
                .configure(&mut scenario())
                .unwrap()
                .to_string()
                .contains("credential-sentinel")
        );
        let mut config = config;
        config["providers"]["deepseek"]["apiKey"] = json!("!echo secret");
        fs::write(path, config.to_string()).unwrap();
        assert!(DeepSeekConnection::from_pi(&temp.0.join("models.json")).is_err());
    }
    #[tokio::test]
    async fn live_http_records_usage_and_never_credentials() {
        let temp = Temp::new();
        let (endpoint, count, server) = mock().await;
        let report = run_deepseek(
            &scenario(),
            &temp.0.join("run"),
            connection(endpoint),
            LiveLimits::default(),
        )
        .await
        .unwrap();
        assert!(report.success);
        assert_eq!(report.mode, "native_policy_live");
        assert_eq!(report.usage.input_tokens, Some(100));
        assert_eq!(report.usage.cache_read_tokens, Some(60));
        assert_eq!(count.load(Ordering::SeqCst), 1);
        for name in [
            "manifest.json",
            "scenario.json",
            "requests.jsonl",
            "responses.jsonl",
            "events.jsonl",
            "context.json",
            "report.json",
        ] {
            assert!(
                !fs::read_to_string(temp.0.join("run").join(name))
                    .unwrap()
                    .contains("credential-sentinel")
            );
        }
        assert_eq!(
            crate::validate(&temp.0.join("run")).unwrap().usage,
            report.usage
        );
        server.abort();
    }
    #[tokio::test]
    async fn request_and_reservation_limits_stop_before_network() {
        let temp = Temp::new();
        let (endpoint, count, server) = mock().await;
        let mut case = scenario();
        case.turns.push("Say done again".into());
        let report = run_deepseek(
            &case,
            &temp.0.join("count"),
            connection(endpoint.clone()),
            LiveLimits {
                max_requests: 1,
                max_reserved_tokens: 100_000,
            },
        )
        .await
        .unwrap();
        assert!(!report.success);
        assert_eq!(count.load(Ordering::SeqCst), 1);
        let report = run_deepseek(
            &scenario(),
            &temp.0.join("bound"),
            connection(endpoint),
            LiveLimits {
                max_requests: 1,
                max_reserved_tokens: 1,
            },
        )
        .await
        .unwrap();
        assert!(!report.success);
        assert_eq!(count.load(Ordering::SeqCst), 1);
        server.abort();
    }
    #[tokio::test]
    async fn existing_run_is_never_overwritten_and_reasoning_mapping_is_explicit() {
        let temp = Temp::new();
        let path = temp.0.join("existing");
        fs::create_dir(&path).unwrap();
        fs::write(path.join("requests.jsonl"), "original").unwrap();
        assert!(
            run_deepseek(
                &scenario(),
                &path,
                connection("https://api.deepseek.com/chat/completions".into()),
                LiveLimits::default()
            )
            .await
            .is_err()
        );
        assert_eq!(
            fs::read_to_string(path.join("requests.jsonl")).unwrap(),
            "original"
        );
        let mut case = scenario();
        case.profile.params.reasoning_effort = Some("low".into());
        assert_eq!(
            connection("https://api.deepseek.com/chat/completions".into())
                .configure(&mut case)
                .unwrap()["reasoning_effort"],
            "high"
        );
    }
}
