//! Product execution limits and compaction lifecycle.
use super::*;

#[derive(Clone, Deserialize)]
pub struct Config {
    pub api_key: String,
    pub model: String,
    pub request_limit: u64,
    pub max_tokens: u64,
    pub max_requests_per_turn: u64,
    #[serde(default = "default_parallel_tasks")]
    pub max_parallel_tasks: usize,
}
pub fn default_parallel_tasks() -> usize {
    5
}
impl Default for Config {
    fn default() -> Self {
        Self {
            api_key: String::new(),
            model: String::new(),
            request_limit: 10000,
            max_tokens: 8192,
            max_requests_per_turn: 100,
            max_parallel_tasks: 5,
        }
    }
}
/// Product execution limits do not imply a legacy DeepSeek connection.
#[derive(serde::Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ExecutionLimits {
    request_limit: u64,
    max_tokens: u64,
    max_requests_per_turn: u64,
    max_parallel_tasks: usize,
}
pub fn execution_config(mut base: Config, data: &std::path::Path) -> Result<Config> {
    let path = data.join("execution-limits.json");
    if path.exists() {
        let limits: ExecutionLimits = serde_json::from_slice(&std::fs::read(path)?)?;
        anyhow::ensure!(
            limits.request_limit <= 1_000_000
                && (1..=9_007_199_254_740_991).contains(&limits.max_tokens)
                && (1..=1000).contains(&limits.max_requests_per_turn)
                && (1..=1000).contains(&limits.max_parallel_tasks),
            "Invalid execution limits"
        );
        base.request_limit = limits.request_limit;
        base.max_tokens = limits.max_tokens;
        base.max_requests_per_turn = limits.max_requests_per_turn;
        base.max_parallel_tasks = limits.max_parallel_tasks;
    }
    Ok(base)
}
pub use pot_driver::private_json;
pub const MAX_MODEL_BODY: usize = 16 * 1024 * 1024;

pub async fn compaction_event(
    State(root): State<Service>,
    h: HeaderMap,
    Json(event): Json<Value>,
) -> ApiResult<Json<Value>> {
    let s = tasks::execution_service(&root, &h)?;
    let inner = s.inner.lock().unwrap();
    let turn = inner
        .turn
        .as_ref()
        .ok_or_else(|| error(StatusCode::CONFLICT, "no_turn", "No active turn"))?;
    if turn.cancelling && event["action"] == "start" {
        return Err(error(
            StatusCode::CONFLICT,
            "cancelling",
            "Turn is stopping",
        ));
    }
    s.usage
        .lock()
        .unwrap()
        .compaction(&s.usage_scope, &turn.user_message_id, &event, now())
        .map_err(internal)?;
    Ok(Json(json!({"ok":true})))
}

impl pot_core::ProfileConfig for Config {
    fn model(&self) -> &str {
        &self.model
    }
    fn max_tokens(&self) -> u64 {
        self.max_tokens
    }
}
#[cfg(test)]
pub async fn gateway(
    state: State<Service>,
    headers: HeaderMap,
    body: Json<Value>,
) -> ApiResult<axum::response::Response> {
    crate::gateway::forward(state, headers, body).await
}
#[cfg(test)]
pub async fn run(s: &Service, req: SendMessage) -> Result<()> {
    agent::run(s, req).await
}

/// Owner settings persist on the Server machine; new hosting applies them on restart.
pub async fn limits_get(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let config =
        execution_config(s.execution.clone().unwrap_or_default(), &s.data).map_err(internal)?;
    Ok(Json(
        json!({"request_limit":config.request_limit,"max_tokens":config.max_tokens,
        "max_requests_per_turn":config.max_requests_per_turn,"max_parallel_tasks":config.max_parallel_tasks}),
    ))
}
pub async fn limits_save(
    State(s): State<Service>,
    h: HeaderMap,
    Json(v): Json<Value>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let limits: ExecutionLimits = serde_json::from_value(v).map_err(|_| {
        error(
            StatusCode::BAD_REQUEST,
            "limits",
            "Invalid execution limits",
        )
    })?;
    if limits.request_limit > 1_000_000
        || !(1..=9_007_199_254_740_991).contains(&limits.max_tokens)
        || !(1..=1000).contains(&limits.max_requests_per_turn)
        || !(1..=1000).contains(&limits.max_parallel_tasks)
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "limits",
            "Invalid execution limits",
        ));
    }
    private_json(
        &s.data.join("execution-limits.json"),
        &serde_json::to_value(&limits).map_err(|e| internal(e.into()))?,
    )
    .map_err(internal)?;
    Ok(Json(json!({"saved":true,"restart_required":true})))
}
