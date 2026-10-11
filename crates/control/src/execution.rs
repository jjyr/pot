//! Product execution limits and compaction lifecycle.
use super::*;

#[derive(Clone, Deserialize)]
pub struct Config {
    #[cfg(test)]
    pub api_key: String,
    pub model: String,
    pub request_limit: u64,
    pub max_tokens: u64,
    pub max_requests_per_turn: u64,
    #[serde(default = "default_parallel_tasks")]
    pub max_parallel_tasks: usize,
}
pub fn default_parallel_tasks() -> usize {
    8
}
impl Default for Config {
    fn default() -> Self {
        Self {
            #[cfg(test)]
            api_key: String::new(),
            model: String::new(),
            request_limit: 10000,
            max_tokens: 8192,
            max_requests_per_turn: 100,
            max_parallel_tasks: default_parallel_tasks(),
        }
    }
}
/// Product execution limits do not imply a legacy DeepSeek connection.
#[derive(Deserialize)]
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
                && limits.max_parallel_tasks > 0,
            "Invalid execution limits"
        );
        base.request_limit = limits.request_limit;
        base.max_tokens = limits.max_tokens;
        base.max_requests_per_turn = limits.max_requests_per_turn;
        base.max_parallel_tasks = limits.max_parallel_tasks;
    }
    Ok(base)
}
pub use pot_sandbox::private_json;
pub const MAX_MODEL_BODY: usize = 16 * 1024 * 1024;

pub async fn compaction_event(
    State(root): State<Service>,
    h: HeaderMap,
    Json(event): Json<Value>,
) -> ApiResult<Json<Value>> {
    let s = tasks::execution_service(&root, &h)?;
    record_compaction(&s, event)
}

pub(crate) fn record_compaction(s: &Service, event: Value) -> ApiResult<Json<Value>> {
    let inner = s.conversation.inner.lock().unwrap();
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn concurrency_accepts_the_full_positive_usize_range() {
        let data =
            std::env::temp_dir().join(format!("pot-execution-limits-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir_all(&data).unwrap();
        assert_eq!(
            execution_config(Config::default(), &data)
                .unwrap()
                .max_parallel_tasks,
            8
        );
        for parallel in [1, 1001, usize::MAX, 0] {
            std::fs::write(
                data.join("execution-limits.json"),
                serde_json::to_vec(&json!({
                    "request_limit": 10000,
                    "max_tokens": 8192,
                    "max_requests_per_turn": 100,
                    "max_parallel_tasks": parallel,
                }))
                .unwrap(),
            )
            .unwrap();
            let config = execution_config(Config::default(), &data);
            if parallel == 0 {
                assert!(config.is_err());
            } else {
                assert_eq!(config.unwrap().max_parallel_tasks, parallel);
            }
        }
        std::fs::remove_dir_all(data).unwrap();
    }
}
