//! Unit fixtures for private Agent state transitions; no HTTP service or paid calls.
use super::*;
use crate::extensions::Defaults;
use std::{
    collections::VecDeque,
    path::PathBuf,
    sync::{
        Mutex,
        atomic::{AtomicBool, Ordering},
    },
};
use tokio::sync::Notify;

#[derive(Default)]
struct Feedback {
    saved: Mutex<Option<Value>>,
    observations: Mutex<Vec<ExecutionObservation>>,
    cancelled: AtomicBool,
    changed: Notify,
    fail_save: Mutex<Option<SaveFault>>,
}
#[derive(Clone, Copy)]
enum SaveFault {
    Any,
    ToolStarted,
    Receipt,
}
impl ExecutionFeedback for Feedback {
    fn observe(&self, event: &ExecutionObservation) -> Result<()> {
        self.observations.lock().unwrap().push(event.clone());
        Ok(())
    }
    fn load_context(&self) -> Result<Option<Value>> {
        Ok(self.saved.lock().unwrap().clone())
    }
    fn save_context(&self, value: &Value) -> Result<()> {
        let fail = match *self.fail_save.lock().unwrap() {
            Some(SaveFault::Any) => true,
            Some(SaveFault::ToolStarted) => value["started"].is_string(),
            Some(SaveFault::Receipt) => value["receipts"].as_array().is_some_and(|r| !r.is_empty()),
            None => false,
        };
        ensure!(!fail, "Injected persistence failure");
        *self.saved.lock().unwrap() = Some(value.clone());
        Ok(())
    }
    fn cancelled(&self) -> bool {
        self.cancelled.load(Ordering::SeqCst)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(async move {
            let changed = self.changed.notified();
            if !self.cancelled() {
                changed.await;
            }
        })
    }
    fn native_identity(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn binding_broken(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn needs_rebind(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn begin_usage(&self, _: &str, _: &ResolvedSpec, _: u64) -> Result<()> {
        Ok(())
    }
    fn initialize_usage(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn cumulative_usage(&self, _: &str, _: &str, _: &Value) -> Result<()> {
        Ok(())
    }
    fn incomplete_usage(&self, _: &str) -> Result<()> {
        Ok(())
    }
}
type Chunks = Vec<std::result::Result<Vec<u8>, String>>;
#[derive(Default)]
struct Services {
    replies: Mutex<VecDeque<Chunks>>,
    requests: Mutex<Vec<Value>>,
    lifecycle: Mutex<Vec<Value>>,
    reject_complete: AtomicBool,
}
impl Services {
    fn queue(&self, frames: Vec<Value>) {
        self.replies.lock().unwrap().push_back(
            frames
                .into_iter()
                .map(|v| Ok(format!("data: {v}\n\n").into_bytes()))
                .collect(),
        );
    }
    fn broken(&self, frames: Vec<Value>) {
        let mut chunks = frames
            .into_iter()
            .map(|v| Ok(format!("data: {v}\n\n").into_bytes()))
            .collect::<Chunks>();
        chunks.push(Err("Injected stream failure".into()));
        self.replies.lock().unwrap().push_back(chunks);
    }
}
impl ExecutionServices for Services {
    fn model(
        &self,
        request: ModelRequest,
    ) -> BoxFuture<'_, Result<futures::stream::BoxStream<'_, Result<Vec<u8>>>>> {
        Box::pin(async move {
            self.requests.lock().unwrap().push(
                json!({"id":request.id,"body":request.body,"compaction":request.compaction_id}),
            );
            let chunks = self
                .replies
                .lock()
                .unwrap()
                .pop_front()
                .ok_or_else(|| anyhow!("Unexpected model request"))?;
            Ok(
                futures::stream::iter(chunks.into_iter().map(|c| c.map_err(|e| anyhow!("{e}"))))
                    .boxed(),
            )
        })
    }
    fn rpc<'a>(
        &'a self,
        _: Option<&'a str>,
        _: &'a str,
        _: &'a str,
        _: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        Box::pin(async { bail!("Unexpected external RPC") })
    }
    fn compaction(&self, event: Value) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            let complete = event["action"] == "complete";
            self.lifecycle.lock().unwrap().push(event);
            ensure!(
                !complete || !self.reject_complete.load(Ordering::SeqCst),
                "Injected completion acknowledgement failure"
            );
            Ok(())
        })
    }
}
struct Fixture {
    dir: PathBuf,
    feedback: Arc<Feedback>,
    context: ExecutionContext,
    binding: Binding,
    services: Services,
}
struct Config;
impl ProfileConfig for Config {
    fn model(&self) -> &str {
        "fixture"
    }
    fn max_tokens(&self) -> u64 {
        128
    }
}
impl Fixture {
    fn new(protocol: &str) -> Self {
        let dir = std::env::temp_dir().join(format!("pot-agent-unit-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&dir).unwrap();
        let feedback = Arc::new(Feedback::default());
        let context = ExecutionContext {
            data: dir.clone(),
            workspace: dir.clone(),
            owner: "unit".into(),
            role: ExecutionRole::Worker,
            backend_url: String::new(),
            lane_token: String::new(),
            main_prompt: String::new(),
            feedback: feedback.clone(),
        };
        let mut profile = Profile::api(&Config);
        profile.params.agent.auto_compact = false;
        profile.params.agent.system_prompt = Some("Complete the task.".into());
        profile.params.agent.builtin_tools = vec!["exec_command".into()];
        let mut spec = profile.resolve().unwrap();
        spec.set_gateway_protocol(protocol).unwrap();
        let binding = Binding {
            pot_owner: "unit".into(),
            epoch: 1,
            spec,
            native_ref: NativeRef::Pot {
                file: dir.join("context.json"),
                session_id: None,
            },
            schema_version: SCHEMA_VERSION,
            state: BindingState::Active,
            model_switches: vec![],
            handoff_ref: None,
            failure: None,
        };
        Self {
            dir,
            feedback,
            context,
            binding,
            services: Services::default(),
        }
    }
    async fn turn(&self, resuming: bool) -> Result<Vec<Update>> {
        let mut updates = vec![];
        let mut processes = processes::Processes::default();
        let result = run(
            &self.context,
            &self.binding,
            &TurnRequest {
                request_id: "turn".into(),
                text: "task".into(),
            },
            resuming,
            &Defaults,
            Some(&self.services),
            &mut processes,
            &mut |u| {
                updates.push(u);
                Ok(())
            },
        )
        .await;
        processes.shutdown().await?;
        result?;
        Ok(updates)
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.dir);
    }
}
fn context(messages: Vec<Message>) -> Context {
    Context {
        kind: "session".into(),
        version: 1,
        id: "unit-context".into(),
        messages,
        pending: vec![],
        started: None,
        last_request: "turn".into(),
        receipts: vec![],
        steering_ids: vec![],
        usage_anchor: None,
    }
}
fn call(id: &str) -> model::ToolCall {
    model::ToolCall {
        id: id.into(),
        name: "exec_command".into(),
        arguments: json!({"cmd":"printf 'once\\n' >> effects.txt"}),
    }
}
fn wire(protocol: &str, text: &str, calls: &[model::ToolCall]) -> Vec<Value> {
    match protocol {
        "openai_chat" => vec![
            json!({"choices":[{"delta":{"content":text,"tool_calls":calls.iter().enumerate().map(|(i,c)|json!({"index":i,"id":c.id,"type":"function","function":{"name":c.name,"arguments":c.arguments.to_string()}})).collect::<Vec<_>>()},"finish_reason":null}]}),
            json!({"choices":[{"delta":{},"finish_reason":if calls.is_empty(){"stop"}else{"tool_calls"}}]}),
            json!({"choices":[],"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":40},"completion_tokens_details":{"reasoning_tokens":5}}}),
        ],
        "openai_responses" => {
            let mut output = vec![
                json!({"id":"msg","type":"message","role":"assistant","content":[{"type":"output_text","text":text}]}),
            ];
            output.extend(calls.iter().map(|c|json!({"type":"function_call","call_id":c.id,"name":c.name,"arguments":c.arguments.to_string()})));
            vec![
                json!({"type":"response.completed","response":{"status":"completed","output":output,"usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":40},"output_tokens_details":{"reasoning_tokens":5}}}}),
            ]
        }
        "anthropic_messages" => {
            let mut frames = vec![
                json!({"type":"message_start","message":{"usage":{"input_tokens":60,"cache_read_input_tokens":30,"cache_creation_input_tokens":10,"output_tokens":0}}}),
                json!({"type":"content_block_start","index":0,"content_block":{"type":"text","text":text}}),
            ];
            frames.extend(calls.iter().enumerate().map(|(i,c)|json!({"type":"content_block_start","index":i+1,"content_block":{"type":"tool_use","id":c.id,"name":c.name,"input":c.arguments}})));
            frames.extend([json!({"type":"message_delta","delta":{"stop_reason":if calls.is_empty(){"end_turn"}else{"tool_use"}},"usage":{"output_tokens":20}}),json!({"type":"message_stop"})]);
            frames
        }
        "gemini" => {
            let mut parts = vec![json!({"text":text})];
            parts.extend(calls.iter().map(|c|json!({"functionCall":{"id":c.id,"name":c.name,"args":c.arguments},"thoughtSignature":"signature"})));
            vec![
                json!({"candidates":[{"content":{"role":"model","parts":parts},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":15,"thoughtsTokenCount":5,"cachedContentTokenCount":40}}),
            ]
        }
        _ => panic!(),
    }
}
const PROTOCOLS: [&str; 4] = [
    "openai_chat",
    "openai_responses",
    "anthropic_messages",
    "gemini",
];
struct Estimate(u64);
impl AgentExtensions for Estimate {
    fn estimate_tokens(
        &self,
        _: &Protocol,
        _: &str,
        _: &str,
        _: &[Message],
        _: &[Value],
    ) -> Result<u64> {
        Ok(self.0)
    }
}
fn compact_history() -> Context {
    context(vec![
        Message::text("user", "old facts ".repeat(100)),
        Message::text("assistant", "old reply".into()),
        Message::text("user", "current constraint".into()),
        Message::text("assistant", "current reply".into()),
    ])
}

#[tokio::test]
async fn compaction_threshold_exact_boundary_minimum_history_and_disabled_policy() {
    for (estimate, len, enabled, trigger) in [
        (99, 4, true, false),
        (100, 4, true, true),
        (101, 4, true, true),
        (100, 3, true, false),
        (100, 4, false, false),
    ] {
        let mut f = Fixture::new("openai_chat");
        f.binding.spec.params.agent.auto_compact = enabled;
        f.binding.spec.params.agent.compact_at = Some(100);
        let mut state = compact_history();
        state.messages.truncate(len);
        f.services.queue(wire("openai_chat", "summary", &[]));
        compact(
            Some(&f.services),
            &client(1000).unwrap(),
            &f.context,
            &f.binding,
            &mut state,
            &mut |_| Ok(()),
            &Estimate(estimate),
            "system",
            &[],
        )
        .await
        .unwrap();
        assert_eq!(
            f.services.requests.lock().unwrap().len(),
            usize::from(trigger),
            "estimate={estimate}, len={len}, enabled={enabled}"
        );
        if trigger {
            assert_eq!(state.messages[1].text, "current constraint");
            assert_eq!(state.messages[2].text, "current reply");
            assert_eq!(
                f.services.lifecycle.lock().unwrap()[1]["action"],
                "complete"
            );
        }
    }
}
#[tokio::test]
async fn compaction_derived_threshold_accounts_for_output_and_context_reservations() {
    for (output, expected) in [(128, 852), (400, 730)] {
        let mut f = Fixture::new("openai_chat");
        f.binding.spec.context_window = 1000;
        f.binding.spec.params.max_output_tokens = output;
        f.binding.spec.params.agent.context_reserve = 20;
        f.binding.spec.params.agent.auto_compact = true;
        let mut state = compact_history();
        compact(
            Some(&f.services),
            &client(1000).unwrap(),
            &f.context,
            &f.binding,
            &mut state,
            &mut |_| Ok(()),
            &Estimate(expected - 1),
            "system",
            &[],
        )
        .await
        .unwrap();
        assert!(f.services.requests.lock().unwrap().is_empty());
        assert!(f.feedback.observations.lock().unwrap().iter().any(|e|matches!(e,ExecutionObservation::ContextEstimated{tokens,threshold} if *tokens==expected-1 && *threshold==expected)));
    }
}
#[tokio::test]
async fn compaction_without_a_recent_user_boundary_preserves_history() {
    let mut f = Fixture::new("openai_chat");
    f.binding.spec.params.agent.auto_compact = true;
    f.binding.spec.params.agent.compact_at = Some(1);
    let mut state = context(vec![
        Message::text("user", "only user".into()),
        Message::text("assistant", "a".into()),
        Message::text("tool", "b".into()),
        Message::text("assistant", "c".into()),
    ]);
    let original = serde_json::to_value(&state).unwrap();
    compact(
        Some(&f.services),
        &client(1000).unwrap(),
        &f.context,
        &f.binding,
        &mut state,
        &mut |_| Ok(()),
        &Estimate(100),
        "system",
        &[],
    )
    .await
    .unwrap();
    assert_eq!(serde_json::to_value(state).unwrap(), original);
    assert!(f.services.requests.lock().unwrap().is_empty());
}
#[tokio::test]
async fn provider_calibration_switch_changes_compaction_decision_without_changing_billing() {
    for calibrated in [true, false] {
        let mut f = Fixture::new("openai_chat");
        f.binding.spec.params.agent.auto_compact = true;
        f.binding.spec.params.agent.compact_at = Some(500);
        f.binding.spec.params.agent.use_provider_usage = calibrated;
        let mut state = compact_history();
        state.messages[1].usage = Some(model::TokenUsage {
            input: 100,
            output: 20,
        });
        state.usage_anchor = meter::UsageAnchor::capture(
            &Protocol::OpenaiChat,
            "fixture",
            "system",
            &state.messages[..2],
            &[],
        )
        .unwrap();
        f.services.queue(wire("openai_chat", "summary", &[]));
        compact(
            Some(&f.services),
            &client(1000).unwrap(),
            &f.context,
            &f.binding,
            &mut state,
            &mut |_| Ok(()),
            &Defaults,
            "system",
            &[],
        )
        .await
        .unwrap();
        assert_eq!(
            f.services.requests.lock().unwrap().len(),
            usize::from(!calibrated)
        );
        let events = f.feedback.observations.lock().unwrap();
        assert_eq!(
            events
                .iter()
                .filter(|e| matches!(e, ExecutionObservation::UsageObserved { .. }))
                .count(),
            usize::from(!calibrated)
        );
    }
}
#[tokio::test]
async fn unsuccessful_compaction_keeps_original_history_and_closes_lifecycle() {
    for failure in ["stream", "tools", "no-reduction", "persistence"] {
        let mut f = Fixture::new("openai_chat");
        f.binding.spec.params.agent.auto_compact = true;
        f.binding.spec.params.agent.compact_at = Some(1);
        let mut state = compact_history();
        state.messages[1].usage = Some(model::TokenUsage {
            input: 100,
            output: 20,
        });
        state.usage_anchor = meter::UsageAnchor::capture(
            &Protocol::OpenaiChat,
            "fixture",
            "system",
            &state.messages[..2],
            &[],
        )
        .unwrap();
        let original = serde_json::to_value(&state).unwrap();
        match failure {
            "stream" => f.services.broken(vec![]),
            "tools" => f
                .services
                .queue(wire("openai_chat", "", &[call("forbidden")])),
            "no-reduction" => {
                f.services
                    .queue(wire("openai_chat", &"oversized ".repeat(1000), &[]))
            }
            "persistence" => {
                *f.feedback.fail_save.lock().unwrap() = Some(SaveFault::Any);
                f.services.queue(wire("openai_chat", "summary", &[]));
            }
            _ => unreachable!(),
        }
        assert!(
            compact(
                Some(&f.services),
                &client(1000).unwrap(),
                &f.context,
                &f.binding,
                &mut state,
                &mut |_| Ok(()),
                &Estimate(100),
                "system",
                &[]
            )
            .await
            .is_err(),
            "{failure}"
        );
        assert_eq!(serde_json::to_value(&state).unwrap(), original, "{failure}");
        assert_eq!(
            f.services.lifecycle.lock().unwrap().last().unwrap()["action"],
            "failed",
            "{failure}"
        );
        assert!(!f.dir.join("effects.txt").exists());
    }
}
#[tokio::test]
async fn successful_compaction_clears_anchor_and_excludes_tools_from_summary_request() {
    let mut f = Fixture::new("openai_chat");
    f.binding.spec.params.agent.auto_compact = true;
    f.binding.spec.params.agent.compact_at = Some(1);
    let mut state = compact_history();
    state.messages[1].usage = Some(model::TokenUsage {
        input: 100,
        output: 20,
    });
    state.usage_anchor = meter::UsageAnchor::capture(
        &Protocol::OpenaiChat,
        "fixture",
        "system",
        &state.messages[..2],
        &[],
    )
    .unwrap();
    f.services.queue(wire("openai_chat", "summary", &[]));
    compact(
        Some(&f.services),
        &client(1000).unwrap(),
        &f.context,
        &f.binding,
        &mut state,
        &mut |_| Ok(()),
        &Estimate(100),
        "system",
        &tools::schemas(&ShellSettings::default()),
    )
    .await
    .unwrap();
    assert!(state.usage_anchor.is_none());
    assert_eq!(
        f.feedback.saved.lock().unwrap().as_ref().unwrap()["messages"]
            .as_array()
            .unwrap()
            .len(),
        3
    );
    let requests = f.services.requests.lock().unwrap();
    assert!(
        !requests[0]["body"]
            .as_object()
            .unwrap()
            .contains_key("tools")
    );
    assert!(requests[0]["compaction"].is_string());
}
#[tokio::test]
async fn rejected_completion_ack_and_failed_display_sink_do_not_publish_two_terminals() {
    for reject_ack in [true, false] {
        let mut f = Fixture::new("openai_chat");
        f.binding.spec.params.agent.auto_compact = true;
        f.binding.spec.params.agent.compact_at = Some(1);
        f.services
            .reject_complete
            .store(reject_ack, Ordering::SeqCst);
        f.services.queue(wire("openai_chat", "summary", &[]));
        let mut state = compact_history();
        let result = compact(
            Some(&f.services),
            &client(1000).unwrap(),
            &f.context,
            &f.binding,
            &mut state,
            &mut |_| Err(anyhow!("Injected display failure")),
            &Estimate(100),
            "system",
            &[],
        )
        .await;
        assert!(result.is_err());
        let actions = f
            .feedback
            .observations
            .lock()
            .unwrap()
            .iter()
            .filter_map(|e| match e {
                ExecutionObservation::CompactionLifecycle { action, .. } => Some(action.clone()),
                _ => None,
            })
            .collect::<Vec<_>>();
        assert_eq!(
            actions,
            vec!["start", if reject_ack { "failed" } else { "complete" }]
        );
        assert_eq!(
            f.feedback.saved.lock().unwrap().as_ref().unwrap()["messages"]
                .as_array()
                .unwrap()
                .len(),
            3
        );
    }
}
#[tokio::test]
async fn cumulative_usage_snapshots_cache_and_reasoning_are_not_added_twice() {
    for protocol in PROTOCOLS {
        let f = Fixture::new(protocol);
        let mut frames = wire(protocol, "done", &[]);
        // Re-deliver complete usage snapshots before EOF, as a provider stream may do.
        let usage = frames
            .iter()
            .filter(|e| {
                e["usage"].is_object()
                    || e["response"]["usage"].is_object()
                    || e["usageMetadata"].is_object()
            })
            .map(|e| {
                if protocol == "gemini" {
                    json!({"usageMetadata":e["usageMetadata"]})
                } else {
                    e.clone()
                }
            })
            .collect::<Vec<_>>();
        frames.extend(usage);
        f.services.queue(frames);
        let reply = sample(
            Some(&f.services),
            &client(1000).unwrap(),
            &f.context,
            &f.binding,
            "system",
            &[],
            &[],
            None,
            &mut |_| Ok(()),
        )
        .await
        .unwrap();
        let usage = reply.usage.unwrap();
        assert_eq!((usage.input, usage.output), (100, 20), "{protocol}");
        assert_eq!(reply.text, "done");
        let events = f.feedback.observations.lock().unwrap();
        assert_eq!(
            events
                .iter()
                .filter(|e| matches!(e, ExecutionObservation::RequestStarted { .. }))
                .count(),
            1
        );
        assert!(events.iter().any(|e| matches!(
            e,
            ExecutionObservation::RequestFinished {
                input_tokens: Some(100),
                output_tokens: Some(20),
                error: None,
                ..
            }
        )));
        assert!(events.iter().any(|e|matches!(e,ExecutionObservation::UsageObserved{cache_read_tokens:Some(n),..} if *n==if protocol=="anthropic_messages" {30} else {40})));
        if protocol == "anthropic_messages" {
            assert!(events.iter().any(|e| matches!(
                e,
                ExecutionObservation::UsageObserved {
                    input_tokens: Some(100),
                    output_tokens: None,
                    cache_write_tokens: Some(10),
                    ..
                }
            )));
        } else {
            assert!(events.iter().any(|e| matches!(
                e,
                ExecutionObservation::UsageObserved {
                    reasoning_tokens: Some(5),
                    ..
                }
            )));
        }
    }
}
#[tokio::test]
async fn interrupted_stream_matrix_never_executes_or_persists_incomplete_calls() {
    for protocol in PROTOCOLS {
        for point in ["before", "text", "call", "after-usage"] {
            let f = Fixture::new(protocol);
            let frames = match point {
                "before" => vec![],
                "text" => match protocol {
                    "openai_responses" => {
                        vec![json!({"type":"response.output_text.delta","delta":"partial"})]
                    }
                    "anthropic_messages" => vec![
                        json!({"type":"content_block_start","index":0,"content_block":{"type":"text","text":"partial"}}),
                    ],
                    "gemini" => {
                        vec![json!({"candidates":[{"content":{"parts":[{"text":"partial"}]}}]})]
                    }
                    _ => vec![json!({"choices":[{"delta":{"content":"partial"}}]})],
                },
                "call" => match protocol {
                    "openai_responses" => vec![
                        json!({"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","call_id":"effect","name":"exec_command","arguments":call("effect").arguments.to_string()}}),
                    ],
                    "anthropic_messages" => vec![
                        json!({"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"effect","name":"exec_command","input":call("effect").arguments}}),
                    ],
                    "gemini" => vec![
                        json!({"candidates":[{"content":{"parts":[{"functionCall":{"id":"effect","name":"exec_command","args":call("effect").arguments}}]}}]}),
                    ],
                    _ => vec![wire(protocol, "", &[call("effect")])[0].clone()],
                },
                _ => wire(protocol, "done", &[call("effect")]),
            };
            f.services.broken(frames);
            assert!(f.turn(false).await.is_err(), "{protocol} {point}");
            assert!(!f.dir.join("effects.txt").exists());
            let saved = f.feedback.saved.lock().unwrap().clone().unwrap();
            assert!(saved["pending"].as_array().unwrap().is_empty());
            assert_eq!(saved["messages"].as_array().unwrap().len(), 1);
            let events = f.feedback.observations.lock().unwrap();
            assert!(events.iter().any(|e| matches!(
                e,
                ExecutionObservation::RequestFinished { error: Some(_), .. }
            )));
            if point == "after-usage" {
                assert!(events.iter().any(|e| matches!(
                    e,
                    ExecutionObservation::UsageObserved {
                        input_tokens: Some(100),
                        output_tokens: Some(20),
                        ..
                    }
                )));
            }
        }
    }
}
#[tokio::test]
async fn context_window_guard_includes_output_reservation_and_rejects_before_transport() {
    for (estimate, output, window, accepted) in [
        (872, 128, 1000, true),
        (873, 128, 1000, false),
        (u64::MAX, 128, 1000, false),
    ] {
        let mut f = Fixture::new("openai_chat");
        f.binding.spec.context_window = window;
        f.binding.spec.params.max_output_tokens = output;
        f.services.queue(wire("openai_chat", "done", &[]));
        let mut processes = processes::Processes::default();
        let result = run(
            &f.context,
            &f.binding,
            &TurnRequest {
                request_id: "guard".into(),
                text: "task".into(),
            },
            false,
            &Estimate(estimate),
            Some(&f.services),
            &mut processes,
            &mut |_| Ok(()),
        )
        .await;
        processes.shutdown().await.unwrap();
        assert_eq!(result.is_ok(), accepted);
        assert_eq!(
            f.services.requests.lock().unwrap().len(),
            usize::from(accepted)
        );
    }
}
#[tokio::test]
async fn interrupted_started_and_unstarted_tool_calls_are_not_replayed_after_reopen() {
    let f = Fixture::new("openai_chat");
    let mut state = context(vec![Message::text("user", "task".into())]);
    state.pending = vec![call("started"), call("unstarted")];
    state.started = Some("started".into());
    state.save(&f.context).unwrap();
    std::fs::write(f.dir.join("effects.txt"), "once\n").unwrap();
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn(true).await.unwrap();
    assert_eq!(
        std::fs::read_to_string(f.dir.join("effects.txt")).unwrap(),
        "once\n"
    );
    let saved = f.feedback.saved.lock().unwrap().clone().unwrap();
    let messages = saved["messages"].as_array().unwrap();
    assert!(
        messages[1]["text"]
            .as_str()
            .unwrap()
            .contains("effects may have occurred")
    );
    assert!(
        messages[2]["text"]
            .as_str()
            .unwrap()
            .contains("Not executed")
    );
    assert_eq!(messages.iter().filter(|m| m["role"] == "user").count(), 1);
}
#[tokio::test]
async fn failed_persistence_before_tool_start_prevents_the_side_effect() {
    let f = Fixture::new("openai_chat");
    *f.feedback.fail_save.lock().unwrap() = Some(SaveFault::ToolStarted);
    f.services.queue(wire("openai_chat", "", &[call("effect")]));
    assert!(f.turn(false).await.is_err());
    assert!(!f.dir.join("effects.txt").exists());
    let saved = f.feedback.saved.lock().unwrap().clone().unwrap();
    assert_eq!(saved["pending"][0]["id"], "effect");
    assert!(saved["started"].is_null());
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn(true).await.unwrap();
    assert!(!f.dir.join("effects.txt").exists());
}
#[tokio::test]
async fn failed_receipt_persistence_after_effect_is_recovered_as_unknown_without_reexecution() {
    let f = Fixture::new("openai_chat");
    *f.feedback.fail_save.lock().unwrap() = Some(SaveFault::Receipt);
    f.services.queue(wire("openai_chat", "", &[call("effect")]));
    assert!(f.turn(false).await.is_err());
    assert_eq!(
        std::fs::read_to_string(f.dir.join("effects.txt")).unwrap(),
        "once\n"
    );
    let checkpoint = f.feedback.saved.lock().unwrap().clone().unwrap();
    assert_eq!(checkpoint["started"], "effect");
    assert!(checkpoint["receipts"].as_array().unwrap().is_empty());
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn(true).await.unwrap();
    assert_eq!(
        std::fs::read_to_string(f.dir.join("effects.txt")).unwrap(),
        "once\n"
    );
    let checkpoint = f.feedback.saved.lock().unwrap().clone().unwrap();
    assert!(checkpoint["messages"].as_array().unwrap().iter().any(|m| {
        m["role"] == "tool"
            && m["call_id"] == "effect"
            && m["text"]
                .as_str()
                .is_some_and(|s| s.contains("effects may have occurred"))
    }));
}
#[tokio::test]
async fn durable_receipt_reopen_reuses_result_and_rejects_changed_arguments() {
    for conflict in [false, true] {
        let f = Fixture::new("openai_chat");
        let original = call("effect");
        let mut state = context(vec![Message::text("user", "task".into())]);
        state.receipts = vec![
            json!({"id":original.id,"name":original.name,"arguments":original.arguments,"result":{"exit_code":0,"stdout":"saved"}}),
        ];
        state.save(&f.context).unwrap();
        std::fs::write(f.dir.join("effects.txt"), "once\n").unwrap();
        let mut repeated = original;
        if conflict {
            repeated.arguments = json!({"cmd":"printf twice >> effects.txt"});
        }
        f.services.queue(wire("openai_chat", "", &[repeated]));
        f.services.queue(wire("openai_chat", "done", &[]));
        let events = f.turn(true).await.unwrap();
        assert_eq!(
            std::fs::read_to_string(f.dir.join("effects.txt")).unwrap(),
            "once\n"
        );
        assert!(
            events
                .iter()
                .any(|u| matches!(u,Update::ToolFinished{failed,..} if *failed==conflict))
        );
    }
}
#[tokio::test]
async fn shell_capacity_independent_input_eof_and_stale_session_ids() {
    let f = Fixture::new("openai_chat");
    let policy = ShellSettings {
        max_sessions: 2,
        default_yield_ms: 5,
        max_yield_ms: 50,
        default_timeout_ms: 2000,
        ..Default::default()
    };
    let mut processes = processes::Processes::default();
    let mut ids = vec![];
    for name in ["left", "right"] {
        let result = processes
            .execute(
                &f.context,
                &policy,
                "exec_command",
                &json!({"cmd":"read line; printf '%s' \"$line\"","yield_time_ms":5}),
                name,
                &mut |_| Ok(()),
            )
            .await
            .unwrap();
        ids.push(result["session_id"].as_str().unwrap().to_owned());
    }
    assert!(
        processes
            .execute(
                &f.context,
                &policy,
                "exec_command",
                &json!({"cmd":"true","yield_time_ms":5}),
                "excess",
                &mut |_| Ok(())
            )
            .await
            .is_err()
    );
    for (id, text) in ids.iter().zip(["left", "right"]) {
        let mut output = String::new();
        let mut args = json!({"session_id":id,"chars":format!("{text}\n"),"close_stdin":true,"yield_time_ms":20});
        let mut complete = false;
        for _ in 0..20 {
            let result = processes
                .execute(
                    &f.context,
                    &policy,
                    "write_stdin",
                    &args,
                    "input",
                    &mut |_| Ok(()),
                )
                .await
                .unwrap();
            output.push_str(result["stdout"].as_str().unwrap());
            args["chars"] = json!("");
            if result["session_id"].is_null() {
                assert_eq!(result["exit_code"], 0);
                complete = true;
                break;
            }
        }
        assert!(complete);
        assert_eq!(output, text);
        assert!(
            processes
                .execute(
                    &f.context,
                    &policy,
                    "write_stdin",
                    &json!({"session_id":id}),
                    "stale",
                    &mut |_| Ok(())
                )
                .await
                .is_err()
        );
    }
    processes.shutdown().await.unwrap();
}
#[tokio::test]
async fn shell_limits_invalid_numeric_arguments_before_spawning_and_truncates_output() {
    let f = Fixture::new("openai_chat");
    let policy = ShellSettings {
        default_output_bytes: 8,
        max_output_bytes: 8,
        max_timeout_ms: 1000,
        default_timeout_ms: 500,
        ..Default::default()
    };
    let mut processes = processes::Processes::default();
    for value in [json!(0), json!(-1), json!(1.5), json!("10"), json!(1001)] {
        assert!(
            processes
                .execute(
                    &f.context,
                    &policy,
                    "exec_command",
                    &json!({"cmd":"touch forbidden","timeout_ms":value}),
                    "bad",
                    &mut |_| Ok(())
                )
                .await
                .is_err()
        );
    }
    assert!(!f.dir.join("forbidden").exists());
    let result = processes
        .execute(
            &f.context,
            &policy,
            "exec_command",
            &json!({"cmd":"printf 0123456789abcdef; printf ABCDEFGHIJKLMNOP >&2"}),
            "bounded",
            &mut |_| Ok(()),
        )
        .await
        .unwrap();
    assert_eq!(result["stdout"], "01234567");
    assert_eq!(result["stderr"], "ABCDEFGH");
    assert_eq!(result["truncated"], true);
    assert_eq!(result["exit_code"], 0);
    processes.shutdown().await.unwrap();
}
#[tokio::test]
async fn cancelling_two_shell_sessions_cleans_both_process_groups() {
    let f = Fixture::new("openai_chat");
    let policy = ShellSettings {
        default_yield_ms: 5,
        max_yield_ms: 50,
        default_timeout_ms: 5000,
        ..Default::default()
    };
    let mut processes = processes::Processes::default();
    let mut ids = vec![];
    for i in 0..2 {
        let result = processes
            .execute(
                &f.context,
                &policy,
                "exec_command",
                &json!({"cmd":format!("echo $$ > pid-{i}; sleep 10"),"yield_time_ms":5}),
                "start",
                &mut |_| Ok(()),
            )
            .await
            .unwrap();
        ids.push(result["session_id"].as_str().unwrap().to_owned());
    }
    for _ in 0..100 {
        if f.dir.join("pid-0").exists() && f.dir.join("pid-1").exists() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(5)).await;
    }
    let pids = (0..2)
        .map(|i| {
            std::fs::read_to_string(f.dir.join(format!("pid-{i}")))
                .unwrap()
                .trim()
                .parse::<i32>()
                .unwrap()
        })
        .collect::<Vec<_>>();
    f.feedback.cancelled.store(true, Ordering::SeqCst);
    f.feedback.changed.notify_waiters();
    processes.shutdown().await.unwrap();
    for pid in pids {
        assert_eq!(unsafe { libc::kill(pid, 0) }, -1);
        assert_eq!(
            std::io::Error::last_os_error().raw_os_error(),
            Some(libc::ESRCH)
        );
    }
    for id in ids {
        assert!(
            processes
                .execute(
                    &f.context,
                    &policy,
                    "write_stdin",
                    &json!({"session_id":id}),
                    "stale",
                    &mut |_| Ok(())
                )
                .await
                .is_err()
        );
    }
}
