//! Unit fixtures for private Agent state transitions; no HTTP service or paid calls.
use super::*;
use crate::extensions::Defaults;
use anyhow::bail;
use futures::future::BoxFuture;
use std::time::Duration;
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
    authorization: Mutex<bool>,
    prepared_execution_target: Mutex<Option<String>>,
    permission_requests: Mutex<Vec<Value>>,
    permission_decision: Mutex<Option<bool>>,
    permission_changed: Notify,
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
    fn resolve_execution_operation(
        &self,
        _: &str,
        mut args: Value,
    ) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            if let Some(execution_target) = &*self.prepared_execution_target.lock().unwrap() {
                args["execution_target"] = json!(execution_target);
            } else if args.get("execution_target").is_none() && *self.authorization.lock().unwrap()
            {
                args["execution_target"] = json!("host");
            }
            Ok(args)
        })
    }
    fn owner_authorization_available(&self) -> Result<bool> {
        Ok(*self.authorization.lock().unwrap())
    }
    fn request_permission(&self, request: Value) -> BoxFuture<'_, Result<Value>> {
        Box::pin(async move {
            {
                let mut requests = self.permission_requests.lock().unwrap();
                if !requests.iter().any(|r| r["call_id"] == request["call_id"]) {
                    requests.push(request.clone());
                }
            }
            loop {
                let notified = self.permission_changed.notified();
                if let Some(approved) = *self.permission_decision.lock().unwrap() {
                    return Ok(json!({"request":request,"approved":approved}));
                }
                notified.await;
            }
        })
    }
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
    fn record_turn(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
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
    rpc_tools: Option<Vec<Value>>,
    rpc_calls: Mutex<Vec<Value>>,
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
        method: &'a str,
        params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        Box::pin(async move {
            self.rpc_calls
                .lock()
                .unwrap()
                .push(json!({"method":method,"params":params}));
            let tools = self
                .rpc_tools
                .as_ref()
                .ok_or_else(|| anyhow!("Unexpected external RPC"))?;
            let result = match method {
                "initialize" => json!({}),
                "tools/list" => json!({"tools":tools}),
                "tools/call" => json!({"managed":true}),
                _ => bail!("Unexpected RPC method"),
            };
            Ok((result, Some("unit-session".into())))
        })
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
    thread: Thread,
    services: Services,
}
struct BrowserFixtureExecutor {
    vision: bool,
}
impl ToolExecutor for BrowserFixtureExecutor {
    fn execute<'a>(
        &'a self,
        _: &'a ExecutionContext,
        _: &'a ShellSettings,
        name: &'a str,
        _: &'a Value,
        _: &'a str,
        _: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            assert_eq!(name, "cua_repl");
            let mut content = vec![json!({"type":"text","text":"Saved Pot"})];
            if self.vision {
                content.push(json!({"type":"image","mimeType":"image/jpeg","data":"AQID","width":1280,"height":800}));
            }
            Ok(json!({"session_id":"browser-session","sequence":1,"content":content}))
        })
    }
    fn finish<'a>(&'a self, _: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { Ok(()) })
    }
}
#[tokio::test]
async fn browser_model_configuration_selects_schema_and_typed_observations() {
    for protocol in [
        "openai_chat",
        "openai_responses",
        "anthropic_messages",
        "gemini",
    ] {
        for vision in [false, true] {
            let mut f = Fixture::new(protocol);
            f.thread.config.params.agent.builtin_tools = vec![];
            f.thread.config.params.agent.model_catalog = Some(serde_json::from_value(json!({"schema_version":1,"default":{"input_modalities":if vision {vec!["text", "image"]} else {vec!["text"]}}})).unwrap());
            f.context.tool_executor = Some(Arc::new(BrowserFixtureExecutor { vision }));
            let calls = vec![model::ToolCall {
                id: "browser-call".into(),
                name: "cua_repl".into(),
                arguments: json!({"code":r#"let tab = await cua.open("https://example.com");"#}),
            }];
            f.services.queue(wire(protocol, "", &calls));
            f.services.queue(wire(protocol, "done", &[]));
            let updates = f.turn(false).await.unwrap();
            let requests = f.services.requests.lock().unwrap();
            // Gemini publishes functionDeclarations rather than top-level function tools.
            let body = &requests[0]["body"];
            let encoded_schema = body["tools"].to_string();
            assert!(encoded_schema.contains("cua_repl"));
            assert!(encoded_schema.contains("code"));
            let continuation = requests[1]["body"].to_string();
            assert!(continuation.contains("Saved Pot"));
            assert_eq!(continuation.contains("AQID"), vision);
            let context: Context =
                serde_json::from_value(f.feedback.load_context().unwrap().unwrap()).unwrap();
            let observation = context.messages.iter().find(|m| m.role == "tool").unwrap();
            assert_eq!(observation.images.len(), usize::from(vision));
            assert!(!observation.text.contains("AQID"));
            for update in updates {
                if let Update::ToolFinished { result, .. } = update {
                    assert!(!result.to_string().contains("AQID"));
                }
            }
        }
    }
}
#[test]
fn image_context_estimate_uses_codex_content_cost_instead_of_base64_text() {
    let mut message = Message::text("tool", "observed page".into());
    message.images.push(model::ModelImage {
        mime_type: "image/jpeg".into(),
        data: "AQID".repeat(200000),
        width: 1280,
        height: 800,
    });
    let tokens = Defaults
        .estimate_tokens(&Protocol::OpenaiChat, "model", "system", &[message], &[])
        .unwrap();
    // Base instructions=2; the tool's 13 text bytes and 7373 image bytes share
    // one logical FunctionCallOutput item, costing 1847 tokens before wrapping.
    assert_eq!(tokens, 1849);
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
            instructions: String::new(),
            context_sections: vec![],
            skill_resources: Default::default(),
            tool_executor: Some(Arc::new(pot_tool_executor::LocalTools::default())),
            data: dir.clone(),
            workspace: dir.clone(),
            owner: "unit".into(),
            role: ExecutionRole::Worker,

            main_prompt: String::new(),
            feedback: feedback.clone(),
        };
        let mut profile = Profile::api(&Config);
        profile.params.agent.auto_compact = false;
        profile.params.agent.system_prompt = Some("Complete the task.".into());
        profile.params.agent.builtin_tools = vec!["exec_command".into()];
        let mut spec = profile.resolve().unwrap();
        spec.set_gateway_protocol(protocol).unwrap();
        let thread = Thread {
            owner: "unit".into(),
            generation: 1,
            config: spec,
        };
        Self {
            dir,
            feedback,
            context,
            thread,
            services: Services::default(),
        }
    }
    async fn turn(&self, resuming: bool) -> Result<Vec<Update>> {
        self.turn_with(resuming, &Defaults).await
    }
    async fn turn_with(
        &self,
        resuming: bool,
        extensions: &dyn AgentExtensions,
    ) -> Result<Vec<Update>> {
        let mut updates = vec![];
        let result = run(
            &self.context,
            &self.thread,
            &TurnRequest {
                request_id: "turn".into(),
                text: "task".into(),
            },
            resuming,
            extensions,
            Some(&self.services),
            &mut |u| {
                updates.push(u);
                Ok(())
            },
        )
        .await;
        if let Some(executor) = &self.context.tool_executor {
            executor.finish(&self.context).await?;
        }
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
        kind: "context".into(),
        version: 2,
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
        f.thread.config.params.agent.auto_compact = enabled;
        f.thread.config.params.agent.compact_at = Some(100);
        let mut state = compact_history();
        state.messages.truncate(len);
        f.services.queue(wire("openai_chat", "summary", &[]));
        compact(
            Some(&f.services),
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Ok(()),
            &Estimate(estimate),
            "system",
            &[],
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
            assert_eq!(state.messages.len(), 2);
            assert_eq!(state.messages[0].kind, model::MessageKind::Summary);
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
        f.thread.config.context_window = 1000;
        f.thread.config.params.max_output_tokens = output;
        f.thread.config.params.agent.context_reserve = 20;
        f.thread.config.params.agent.auto_compact = true;
        let mut state = compact_history();
        compact(
            Some(&f.services),
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Ok(()),
            &Estimate(expected - 1),
            "system",
            &[],
            &[],
        )
        .await
        .unwrap();
        assert!(f.services.requests.lock().unwrap().is_empty());
        assert!(f.feedback.observations.lock().unwrap().iter().any(|e|matches!(e,ExecutionObservation::ContextEstimated{tokens,threshold} if *tokens==expected-1 && *threshold==expected)));
    }
}
#[tokio::test]
async fn compaction_without_a_real_user_preserves_history() {
    let mut f = Fixture::new("openai_chat");
    f.thread.config.params.agent.auto_compact = true;
    f.thread.config.params.agent.compact_at = Some(1);
    let mut state = context(vec![
        Message::text("assistant", "no user".into()),
        Message::text("assistant", "a".into()),
        Message::text("tool", "b".into()),
        Message::text("assistant", "c".into()),
    ]);
    let original = serde_json::to_value(&state).unwrap();
    compact(
        Some(&f.services),
        &f.context,
        &f.thread,
        &mut state,
        &mut |_| Ok(()),
        &Estimate(100),
        "system",
        &[],
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
        f.thread.config.params.agent.auto_compact = true;
        f.thread.config.params.agent.compact_at = Some(200);
        f.thread.config.params.agent.use_provider_usage = calibrated;
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
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Ok(()),
            &Defaults,
            "system",
            &[],
            &[],
        )
        .await
        .unwrap();
        assert_eq!(
            f.services.requests.lock().unwrap().len(),
            usize::from(!calibrated)
        );
        let events = f.feedback.observations.lock().unwrap();
        assert!(events.iter().any(|event| matches!(event,
            ExecutionObservation::ContextEstimated {tokens, threshold}
            if *tokens == if calibrated {129} else {264} && *threshold == 200
        )));
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
    for failure in ["stream", "tools", "empty", "no-reduction", "persistence"] {
        let mut f = Fixture::new("openai_chat");
        f.thread.config.params.agent.auto_compact = true;
        f.thread.config.params.agent.compact_at = Some(1);
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
            "empty" => f.services.queue(wire("openai_chat", "", &[])),
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
                &f.context,
                &f.thread,
                &mut state,
                &mut |_| Ok(()),
                &Estimate(100),
                "system",
                &[],
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
    f.thread.config.params.agent.auto_compact = true;
    f.thread.config.params.agent.compact_at = Some(1);
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
        &f.context,
        &f.thread,
        &mut state,
        &mut |_| Ok(()),
        &Estimate(100),
        "system",
        &tools::schemas(&ShellSettings::default()),
        &[],
    )
    .await
    .unwrap();
    assert!(state.usage_anchor.is_none());
    assert_eq!(
        f.feedback.saved.lock().unwrap().as_ref().unwrap()["messages"]
            .as_array()
            .unwrap()
            .len(),
        2
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
        f.thread.config.params.agent.auto_compact = true;
        f.thread.config.params.agent.compact_at = Some(1);
        f.services
            .reject_complete
            .store(reject_ack, Ordering::SeqCst);
        f.services.queue(wire("openai_chat", "summary", &[]));
        let mut state = compact_history();
        let result = compact(
            Some(&f.services),
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Err(anyhow!("Injected display failure")),
            &Estimate(100),
            "system",
            &[],
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
            2
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
            &f.context,
            &f.thread,
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
            let state: Context = serde_json::from_value(saved.clone()).unwrap();
            assert_eq!(
                state
                    .messages
                    .iter()
                    .filter(|m| m.kind == model::MessageKind::Conversation)
                    .count(),
                1
            );
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
async fn default_context_window_accepts_first_turn_without_counting_bytes_as_tokens() {
    for protocol in [
        "openai_chat",
        "openai_responses",
        "anthropic_messages",
        "gemini",
    ] {
        for (repetitions, accepted) in [(1500, true), (6000, false)] {
            let mut f = Fixture::new(protocol);
            f.context.role = ExecutionRole::Main;
            f.services.rpc_tools = Some(serde_json::from_str(pot_core::TOOL_SCHEMA).unwrap());
            f.thread.config.params.agent = AgentSettings::default();
            f.thread.config.params.max_output_tokens = 8192;
            assert_eq!(f.thread.config.context_window, 32768);
            f.context.instructions = "Follow workspace rules.\n".repeat(repetitions);
            f.services.queue(wire(protocol, "done", &[]));
            let result = f.turn(false).await;
            assert_eq!(result.is_ok(), accepted, "{protocol}: {result:?}");
            let requests = f.services.requests.lock().unwrap();
            assert_eq!(requests.len(), usize::from(accepted));
            if accepted {
                // The old bytes-as-tokens guard rejected this first request. No
                // context-window increase, compaction or paid model call is needed.
                assert!(requests[0]["body"].to_string().len() > 32768);
                assert!(f.services.lifecycle.lock().unwrap().is_empty());
                assert!(f.feedback.observations.lock().unwrap().iter().any(|e|matches!(e,ExecutionObservation::ContextEstimated{tokens,..} if *tokens > 8192 && *tokens + 8192 <= 32768)));
            } else {
                assert!(
                    result
                        .unwrap_err()
                        .to_string()
                        .contains("Context exceeds configured window")
                );
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
        f.thread.config.context_window = window;
        f.thread.config.params.max_output_tokens = output;
        f.services.queue(wire("openai_chat", "done", &[]));
        let result = run(
            &f.context,
            &f.thread,
            &TurnRequest {
                request_id: "guard".into(),
                text: "task".into(),
            },
            false,
            &Estimate(estimate),
            Some(&f.services),
            &mut |_| Ok(()),
        )
        .await;
        if let Some(executor) = &f.context.tool_executor {
            executor.finish(&f.context).await.unwrap();
        }
        assert_eq!(result.is_ok(), accepted);
        assert_eq!(
            f.services.requests.lock().unwrap().len(),
            usize::from(accepted)
        );
    }
}
#[tokio::test]
async fn model_input_limit_is_separate_from_total_context_and_output_limit() {
    for (estimate, accepted) in [(500, true), (501, false)] {
        let mut f = Fixture::new("openai_chat");
        f.thread.config.context_window = 2000;
        f.thread.config.params.max_output_tokens = 128;
        f.thread.config.params.agent.max_input_tokens = Some(500);
        f.services.queue(wire("openai_chat", "done", &[]));
        let result = run(
            &f.context,
            &f.thread,
            &TurnRequest {
                request_id: "input-guard".into(),
                text: "task".into(),
            },
            false,
            &Estimate(estimate),
            Some(&f.services),
            &mut |_| Ok(()),
        )
        .await;
        if let Some(executor) = &f.context.tool_executor {
            executor.finish(&f.context).await.unwrap();
        }
        assert_eq!(result.is_ok(), accepted);
        assert_eq!(
            f.services.requests.lock().unwrap().len(),
            usize::from(accepted)
        );
        if !accepted {
            assert!(
                result
                    .unwrap_err()
                    .to_string()
                    .contains("model input limit")
            );
        }
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
    assert_eq!(
        messages
            .iter()
            .filter(|m| m["role"] == "user" && m["kind"]["type"] == "conversation")
            .count(),
        1
    );
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
    let pids = tokio::time::timeout(Duration::from_secs(2), async {
        loop {
            let pids = (0..2)
                .map(|i| {
                    std::fs::read_to_string(f.dir.join(format!("pid-{i}")))
                        .ok()?
                        .trim()
                        .parse::<i32>()
                        .ok()
                        .filter(|pid| *pid > 0)
                })
                .collect::<Option<Vec<_>>>();
            if let Some(pids) = pids {
                break pids;
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("Both shells must publish their PIDs before cancellation");
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

#[tokio::test]
async fn missing_execution_backend_never_executes_on_host() {
    let mut f = Fixture::new("openai_chat");
    f.context.tool_executor = None;
    f.services.queue(wire("openai_chat", "", &[call("effect")]));
    f.services.queue(wire("openai_chat", "done", &[]));
    let updates = f.turn(false).await.unwrap();
    assert!(!f.dir.join("effects.txt").exists());
    assert!(updates.iter().any(|u|matches!(u, Update::ToolFinished{failed:true,result,..} if result.to_string().contains("no Host fallback"))));
}

fn role_name(role: ExecutionRole) -> &'static str {
    match role {
        ExecutionRole::Main => "main",
        ExecutionRole::Worker => "worker",
        ExecutionRole::Summary => "summary",
    }
}
#[derive(Default)]
struct RecordingExecutor {
    calls: Mutex<Vec<Value>>,
    finishes: Mutex<Vec<String>>,
}
impl pot_core::ToolExecutor for RecordingExecutor {
    fn execute<'a>(
        &'a self,
        context: &'a ExecutionContext,
        policy: &'a ShellSettings,
        name: &'a str,
        args: &'a Value,
        call: &'a str,
        _: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            self.calls
                .lock()
                .unwrap()
                .push(json!({"role":role_name(context.role),
                "owner":context.owner,"policy":policy,"name":name,"args":args,"call":call}));
            Ok(json!({"exit_code":0}))
        })
    }
    fn finish<'a>(&'a self, context: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            self.finishes.lock().unwrap().push(context.owner.clone());
            Ok(())
        })
    }
}
fn model_tools(request: &Value, protocol: &str) -> Vec<Value> {
    request["body"]["tools"]
        .as_array()
        .unwrap()
        .iter()
        .map(|tool| {
            if protocol == "openai_chat" {
                tool["function"].clone()
            } else {
                tool.clone()
            }
        })
        .collect()
}
fn parameters<'a>(tool: &'a Value, protocol: &str) -> &'a Value {
    &tool[if protocol == "anthropic_messages" {
        "input_schema"
    } else {
        "parameters"
    }]
}
fn full_loadout(f: &mut Fixture) {
    f.thread.config.params.agent.builtin_tools = vec![
        "exec_command".into(),
        "write_stdin".into(),
        "apply_patch".into(),
    ];
    let mut control: Vec<Value> = serde_json::from_str(pot_core::TOOL_SCHEMA).unwrap();
    // Shared tool discovery must not execute tools outside the injected backend.
    control.extend(tools::schemas(&f.thread.config.params.agent.shell));
    f.services.rpc_tools = Some(control);
}
#[tokio::test]
async fn main_and_worker_model_loadouts_and_dispatch_use_injected_tools() {
    for protocol in ["openai_chat", "openai_responses", "anthropic_messages"] {
        for role in [ExecutionRole::Main, ExecutionRole::Worker] {
            let mut f = Fixture::new(protocol);
            full_loadout(&mut f);
            f.context.role = role;
            f.thread.config.params.agent.shell.max_output_bytes = 4096;
            let executor = Arc::new(RecordingExecutor::default());
            f.context.tool_executor = Some(executor.clone());
            let calls = vec![
                call("exec"),
                model::ToolCall {
                    id: "patch".into(),
                    name: "apply_patch".into(),
                    arguments: json!({"patch":"*** Begin Patch\n*** Add File: effects.txt\n+patched\n*** End Patch"}),
                },
                model::ToolCall {
                    id: "stdin".into(),
                    name: "write_stdin".into(),
                    arguments: json!({"session_id":"session","chars":"hello"}),
                },
                model::ToolCall {
                    id: "manage".into(),
                    name: "manage_sandbox".into(),
                    arguments: json!({"action":"select","sandbox_id":"linux-b"}),
                },
                model::ToolCall {
                    id: "delegate".into(),
                    name: "create_task".into(),
                    arguments: json!({"text":"task"}),
                },
            ];
            f.services.queue(wire(protocol, "", &calls));
            f.services.queue(wire(protocol, "done", &[]));
            let updates = f.turn(false).await.unwrap();
            let requests = f.services.requests.lock().unwrap();
            let actual = model_tools(&requests[0], protocol);
            let names: std::collections::BTreeSet<_> =
                actual.iter().map(|t| t["name"].as_str().unwrap()).collect();
            assert_eq!(names.len(), actual.len(), "duplicate injected tools");
            let mut expected: std::collections::BTreeSet<String> =
                ["exec_command", "write_stdin", "apply_patch", "cua_repl"]
                    .map(str::to_owned)
                    .into();
            if role == ExecutionRole::Main {
                expected.extend(
                    serde_json::from_str::<Vec<Value>>(pot_core::TOOL_SCHEMA)
                        .unwrap()
                        .iter()
                        .map(|t| t["name"].as_str().unwrap().to_owned()),
                );
            }
            assert_eq!(names, expected.iter().map(String::as_str).collect());
            for name in ["exec_command", "write_stdin", "apply_patch"] {
                let schema = actual.iter().find(|t| t["name"] == name).unwrap();
                assert!(
                    parameters(schema, protocol)["properties"]
                        .get("sandbox_id")
                        .is_none()
                );
            }
            assert_eq!(
                parameters(
                    actual.iter().find(|t| t["name"] == "exec_command").unwrap(),
                    protocol
                )["properties"]["max_output_chars"]["maximum"],
                4096
            );
            for id in ["exec", "patch", "stdin"] {
                assert!(updates.iter().any(
                    |u| matches!(u,Update::ToolFinished{id:actual,failed:false,..} if actual==id)
                ));
            }
            let dispatched = executor.calls.lock().unwrap();
            assert_eq!(dispatched.len(), 3);
            for (record, call) in dispatched.iter().zip(&calls) {
                assert_eq!(
                    *record,
                    json!({"role":role_name(role),"owner":f.context.owner,"policy":f.thread.config.params.agent.shell,"name":call.name,"args":call.arguments,"call":call.id})
                );
            }
            assert_eq!(*executor.finishes.lock().unwrap(), vec!["unit"]);
            let rpc = f.services.rpc_calls.lock().unwrap();
            if role == ExecutionRole::Worker {
                assert!(rpc.is_empty(), "worker fetched or dispatched control tools");
                for id in ["manage", "delegate"] {
                    assert!(updates.iter().any(
                        |u| matches!(u,Update::ToolFinished{id:actual,failed:true,..} if actual==id)
                    ));
                }
            } else {
                let external: Vec<_> = rpc.iter().filter(|r| r["method"] == "tools/call").collect();
                assert_eq!(external.len(), 1);
                assert_eq!(external[0]["params"]["name"], "create_task");
                assert!(updates.iter().any(
                    |u| matches!(u, Update::ToolFinished { id, failed: true, .. } if id == "manage")
                ));
            }
            assert!(
                !f.dir.join("effects.txt").exists(),
                "builtin bypassed injected executor"
            );
        }
    }
}
#[tokio::test]
async fn configured_loadout_restricts_model_tools_and_dispatch() {
    let mut f = Fixture::new("openai_chat");
    full_loadout(&mut f);
    f.context.role = ExecutionRole::Main;
    f.thread.config.params.agent.builtin_tools = vec!["apply_patch".into()];
    f.thread.config.params.agent.allowed_tools =
        Some(vec!["apply_patch".into(), "list_tasks".into()]);
    let executor = Arc::new(RecordingExecutor::default());
    f.context.tool_executor = Some(executor.clone());
    f.services
        .queue(wire("openai_chat", "", &[call("disabled")]));
    f.services.queue(wire("openai_chat", "done", &[]));
    let updates = f.turn(false).await.unwrap();
    let actual = model_tools(&f.services.requests.lock().unwrap()[0], "openai_chat");
    assert_eq!(
        actual
            .iter()
            .map(|t| t["name"].as_str().unwrap())
            .collect::<Vec<_>>(),
        vec!["apply_patch", "list_tasks"]
    );
    assert!(executor.calls.lock().unwrap().is_empty());
    assert!(
        updates
            .iter()
            .any(|u| matches!(u,Update::ToolFinished{failed:true,id,..} if id=="disabled"))
    );
}

#[derive(Default)]
struct CatalogExtension {
    contexts: Mutex<Vec<Value>>,
}
impl AgentExtensions for CatalogExtension {
    fn skills(&self, context: &ExecutionContext) -> Result<Vec<Skill>> {
        self.contexts
            .lock()
            .unwrap()
            .push(json!({"role":role_name(context.role),"owner":context.owner}));
        Ok(vec![Skill {
            name: format!("{}-skill", role_name(context.role)),
            description: "Use for a catalog injection test".into(),
            // Deliberately nonexistent: injection must never read files eagerly.
            location: "/data/skills/example/SKILL.md".into(),
            disable_model_invocation: false,
        }])
    }
}
#[tokio::test]
async fn main_and_task_agents_share_skill_injection_across_model_protocols() {
    for protocol in ["openai_chat", "openai_responses", "anthropic_messages"] {
        for role in [ExecutionRole::Main, ExecutionRole::Worker] {
            let mut f = Fixture::new(protocol);
            full_loadout(&mut f);
            f.context.role = role;
            let catalog = CatalogExtension::default();
            f.services.queue(wire(protocol, "done", &[]));
            f.turn_with(false, &catalog).await.unwrap();
            let captured = f.services.requests.lock().unwrap();
            let body = captured[0]["body"].to_string();
            assert!(body.contains("<available_skills>"));
            assert!(body.contains(&format!("{}-skill", role_name(role))));
            assert!(!body.contains(&format!(
                "{}-skill",
                if role == ExecutionRole::Main {
                    "worker"
                } else {
                    "main"
                }
            )));
            assert!(body.contains("/data/skills/example/SKILL.md"));
            assert!(
                body.contains("Complete the task."),
                "custom system prompt lost"
            );
            assert_eq!(
                *catalog.contexts.lock().unwrap(),
                vec![json!({"role":role_name(role),"owner":"unit"})]
            );
            assert!(
                f.feedback
                    .saved
                    .lock()
                    .unwrap()
                    .as_ref()
                    .unwrap()
                    .to_string()
                    .contains("available_skills"),
                "catalog missing from classified history"
            );
            let tools = model_tools(&captured[0], protocol);
            assert!(
                tools
                    .iter()
                    .all(|t| !t["name"].as_str().unwrap().contains("skill")),
                "skills changed tool permissions"
            );
        }
    }
}
#[tokio::test]
async fn skills_are_not_requested_for_summary_or_without_authorized_reader() {
    for role in [ExecutionRole::Summary, ExecutionRole::Worker] {
        let mut f = Fixture::new("openai_chat");
        f.context.role = role;
        f.thread.config.params.agent.builtin_tools = vec![];
        f.services.rpc_tools = Some(vec![]);
        let catalog = CatalogExtension::default();
        f.services.queue(wire("openai_chat", "done", &[]));
        f.turn_with(false, &catalog).await.unwrap();
        assert!(catalog.contexts.lock().unwrap().is_empty());
        assert!(
            !f.services.requests.lock().unwrap()[0]["body"]
                .to_string()
                .contains("available_skills")
        );
    }
}

#[tokio::test]
async fn filesystem_skills_append_replacements_and_removal_to_history() {
    for role in [ExecutionRole::Main, ExecutionRole::Worker] {
        let mut f = Fixture::new("openai_chat");
        f.context.role = role;
        f.services.rpc_tools = Some(vec![]);
        let bundle = f.dir.join("skills/example");
        std::fs::create_dir_all(&bundle).unwrap();
        f.context.skill_resources.roots = vec![pot_core::SkillRoot {
            path: f.dir.join("skills"),
            location: "/mapped/skills".into(),
            builtin: false,
        }];
        for description in ["first-version", "second-version"] {
            std::fs::write(
                bundle.join("SKILL.md"),
                format!("---\ndescription: {description}\n---\nPRIVATE FULL INSTRUCTIONS"),
            )
            .unwrap();
            f.services.queue(wire("openai_chat", "done", &[]));
            f.turn(false).await.unwrap();
            let requests = f.services.requests.lock().unwrap();
            let body = requests.last().unwrap()["body"].to_string();
            assert!(body.contains(description));
            assert_eq!(
                body.matches("<available_skills>").count(),
                if description == "first-version" { 1 } else { 2 }
            );
            assert!(body.contains("/mapped/skills/example/SKILL.md"));
            assert!(!body.contains("PRIVATE FULL INSTRUCTIONS"));
            if description == "second-version" {
                assert!(body.contains("first-version"));
                assert!(body.contains("replaces all earlier contents"));
            }
            assert!(
                f.feedback
                    .saved
                    .lock()
                    .unwrap()
                    .as_ref()
                    .unwrap()
                    .to_string()
                    .contains("available_skills")
            );
        }
        f.context.skill_resources.selection = Some(vec![]);
        f.services.queue(wire("openai_chat", "done", &[]));
        f.turn(false).await.unwrap();
        assert!(
            f.services.requests.lock().unwrap().last().unwrap()["body"]
                .to_string()
                .contains("previous contents no longer apply")
        );
    }
}

#[tokio::test]
async fn unavailable_selected_skill_fails_before_model_request() {
    let mut f = Fixture::new("openai_chat");
    f.services.rpc_tools = Some(vec![]);
    f.context.skill_resources.selection = Some(vec!["missing".into()]);
    let error = f.turn(false).await.unwrap_err();
    assert!(format!("{error:#}").contains("Selected skill unavailable: missing"));
    assert!(f.services.requests.lock().unwrap().is_empty());
}

#[tokio::test]
async fn allowed_tools_excluding_reader_skips_skill_provider() {
    let mut f = Fixture::new("openai_chat");
    full_loadout(&mut f);
    f.thread.config.params.agent.allowed_tools = Some(vec!["apply_patch".into()]);
    let catalog = CatalogExtension::default();
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn_with(false, &catalog).await.unwrap();
    assert!(catalog.contexts.lock().unwrap().is_empty());
    let requests = f.services.requests.lock().unwrap();
    assert!(!requests[0]["body"].to_string().contains("available_skills"));
    assert_eq!(model_tools(&requests[0], "openai_chat").len(), 1);
}

#[tokio::test]
async fn injected_instructions_append_replacements_for_main_and_worker() {
    for protocol in ["openai_chat", "openai_responses", "anthropic_messages"] {
        for role in [ExecutionRole::Main, ExecutionRole::Worker] {
            let mut f = Fixture::new(protocol);
            f.context.role = role;
            f.services.rpc_tools = Some(vec![]);
            // Directory instructions are relevant even without an authorized reader.
            f.thread.config.params.agent.builtin_tools.clear();
            for text in ["FIRST_INSTRUCTIONS", "UPDATED_INSTRUCTIONS"] {
                f.context.instructions =
                    format!("# AGENTS.md instructions for /data/workspace\n{text}");
                f.services.queue(wire(protocol, "done", &[]));
                f.turn(false).await.unwrap();
                let requests = f.services.requests.lock().unwrap();
                let body = requests.last().unwrap()["body"].to_string();
                assert!(body.contains(text));
                assert!(body.contains("Complete the task."));
                assert_eq!(
                    body.matches("# AGENTS.md instructions for").count(),
                    if text == "FIRST_INSTRUCTIONS" { 1 } else { 2 }
                );
                if text == "UPDATED_INSTRUCTIONS" {
                    assert!(body.contains("FIRST_INSTRUCTIONS"));
                }
                assert!(
                    f.feedback
                        .saved
                        .lock()
                        .unwrap()
                        .as_ref()
                        .unwrap()
                        .to_string()
                        .contains(text)
                );
            }
        }
    }
}
#[tokio::test]
async fn summary_does_not_load_directory_instructions() {
    let mut f = Fixture::new("openai_chat");
    f.context.role = ExecutionRole::Summary;
    f.context.instructions = "DO_NOT_INJECT".into();
    f.services.rpc_tools = Some(vec![]);
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn(false).await.unwrap();
    assert!(
        !f.services.requests.lock().unwrap()[0]["body"]
            .to_string()
            .contains("DO_NOT_INJECT")
    );
}

#[tokio::test]
async fn instructions_are_caller_owned_and_never_discovered_from_agent_cwd() {
    let mut f = Fixture::new("openai_chat");
    std::fs::write(f.dir.join("AGENTS.md"), "WRONG_HOST_DOCUMENT").unwrap();
    f.context.instructions = "\n\nCALLER_INSTRUCTIONS".into();
    f.services.rpc_tools = Some(vec![]);
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn(false).await.unwrap();
    let body = f.services.requests.lock().unwrap()[0]["body"].to_string();
    assert!(body.contains("CALLER_INSTRUCTIONS"));
    assert!(!body.contains("WRONG_HOST_DOCUMENT"));
}

#[tokio::test]
async fn host_write_waits_for_owner_and_denial_has_no_effect() {
    let mut f = Fixture::new("openai_chat");
    f.thread.config.params.agent.builtin_tools = vec![
        "exec_command".into(),
        "write_stdin".into(),
        "apply_patch".into(),
        "exec".into(),
        "wait".into(),
    ];
    *f.feedback.authorization.lock().unwrap() = true;
    let path = f.dir.canonicalize().unwrap().join("owner-file.txt");
    let operation = model::ToolCall {
        id: "host-write".into(),
        name: "exec_command".into(),
        arguments: json!({"cmd":"printf 'approved content' > owner-file.txt"}),
    };
    f.services.queue(wire("openai_chat", "", &[operation]));
    f.services.queue(wire("openai_chat", "done", &[]));
    let decide = async {
        tokio::time::timeout(std::time::Duration::from_secs(2), async {
            while f.feedback.permission_requests.lock().unwrap().is_empty() {
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap();
        assert!(!path.exists(), "Host write occurred before approval");
        let request = f.feedback.permission_requests.lock().unwrap()[0].clone();
        assert_eq!(request["owner"], "unit");
        assert_eq!(request["execution_target"], "host");
        assert_eq!(request["args"]["execution_target"], "host");
        assert!(request.get("proposed_grant").is_none());
        assert_eq!(
            request["args"]["cmd"],
            "printf 'approved content' > owner-file.txt"
        );
        let captured = f.services.requests.lock().unwrap();
        let tools = model_tools(&captured[0], "openai_chat");
        for name in ["exec_command", "write_stdin", "apply_patch", "exec", "wait"] {
            assert_eq!(tools.iter().filter(|t| t["name"] == name).count(), 1);
        }
        assert!(
            !tools
                .iter()
                .any(|t| t["name"].as_str().is_some_and(|n| n.starts_with("host_")))
        );
        drop(captured);
        *f.feedback.permission_decision.lock().unwrap() = Some(false);
        f.feedback.permission_changed.notify_waiters();
    };
    let (updates, ()) = tokio::join!(f.turn(false), decide);
    assert!(!path.exists());
    assert!(
        updates
            .unwrap()
            .iter()
            .any(|u| matches!(u, Update::ToolFinished { failed: true, .. }))
    );
}
#[tokio::test]
async fn execution_checks_authority_for_each_call_and_receipts_prevent_reexecution() {
    let f = Fixture::new("openai_chat");
    *f.feedback.authorization.lock().unwrap() = true;
    *f.feedback.permission_decision.lock().unwrap() = Some(true);
    let path = f.dir.canonicalize().unwrap().join("once.txt");
    let call = model::ToolCall {
        id: "once".into(),
        name: "exec_command".into(),
        arguments: json!({"cmd":"printf first >> once.txt"}),
    };
    f.services
        .queue(wire("openai_chat", "", std::slice::from_ref(&call)));
    f.services.queue(wire(
        "openai_chat",
        "",
        &[
            call.clone(),
            model::ToolCall {
                id: "next".into(),
                ..call
            },
        ],
    ));
    f.services.queue(wire("openai_chat", "done", &[]));
    f.turn(false).await.unwrap();
    assert_eq!(f.feedback.permission_requests.lock().unwrap().len(), 2);
    assert_eq!(std::fs::read_to_string(path).unwrap(), "firstfirst");
}

#[tokio::test]
async fn approved_host_shell_has_bounded_output_and_timeout() {
    let f = Fixture::new("openai_chat");
    *f.feedback.authorization.lock().unwrap() = true;
    *f.feedback.permission_decision.lock().unwrap() = Some(true);
    let root = f.dir.canonicalize().unwrap();
    f.services.queue(wire("openai_chat", "", &[
        model::ToolCall {id:"shell-ok".into(),name:"exec_command".into(),arguments:json!({"cmd":"printf shell-ok > host-result.txt; printf output","workdir":root})},
        model::ToolCall {id:"shell-timeout".into(),name:"exec_command".into(),arguments:json!({"cmd":"sleep 20; printf bad > delayed.txt","workdir":root,"timeout_ms":20})}
    ]));
    f.services.queue(wire("openai_chat", "done", &[]));
    let updates = f.turn(false).await.unwrap();
    assert_eq!(
        std::fs::read_to_string(root.join("host-result.txt")).unwrap(),
        "shell-ok"
    );
    assert!(!root.join("delayed.txt").exists());
    assert!(
        updates
            .iter()
            .any(|u| matches!(u,Update::ToolFinished{id,failed:true,..} if id == "shell-timeout"))
    );
}

#[tokio::test]
async fn composed_shell_calls_use_owner_approval_and_hide_intermediate_results() {
    for approved in [false, true] {
        let mut f = Fixture::new("openai_chat");
        f.thread
            .config
            .params
            .agent
            .builtin_tools
            .extend(["exec".into(), "wait".into()]);
        *f.feedback.authorization.lock().unwrap() = true;
        *f.feedback.permission_decision.lock().unwrap() = Some(approved);
        f.services.queue(wire("openai_chat","",&[model::ToolCall {
            id:"composition".into(),name:"exec".into(),
            arguments:json!({"code":"const r = await tools.exec_command({cmd:'printf PRIVATE_OUTPUT; printf effect > composed-effect'}); text(r.isError ? 'denied' : 'done');","yield_time_ms":1000})
        }]));
        f.services.queue(wire("openai_chat", "done", &[]));
        let updates = f.turn(false).await.unwrap();
        assert_eq!(f.dir.join("composed-effect").exists(), approved);
        let requests = f.feedback.permission_requests.lock().unwrap();
        assert_eq!(requests.len(), 1);
        assert_eq!(requests[0]["tool"], "exec_command");
        assert_eq!(requests[0]["call_id"], "composition:js:1");
        let saved = f.feedback.saved.lock().unwrap();
        let tool = saved.as_ref().unwrap()["messages"]
            .as_array()
            .unwrap()
            .iter()
            .find(|m| m["call_id"] == "composition")
            .unwrap();
        assert!(!tool.to_string().contains("PRIVATE_OUTPUT"));
        assert!(
            updates
                .iter()
                .any(|u| matches!(u,Update::ToolFinished{name,..} if name=="exec"))
        );
    }
}

#[tokio::test]
async fn removed_file_tools_and_disallowed_composed_tools_cannot_execute() {
    let mut f = Fixture::new("openai_chat");
    f.thread
        .config
        .params
        .agent
        .builtin_tools
        .extend(["exec".into(), "wait".into()]);
    f.thread.config.params.agent.allowed_tools = Some(vec!["exec".into(), "wait".into()]);
    let calls = ["read_file", "write_file", "list_directory"]
        .into_iter()
        .map(|name| model::ToolCall {
            id: name.into(),
            name: name.into(),
            arguments: json!({"path":f.dir.join("removed"),"content":"bad"}),
        })
        .chain(std::iter::once(model::ToolCall {
            id: "composed".into(),
            name: "exec".into(),
            arguments: json!({"code":"await tools.exec_command({cmd:'touch removed'});"}),
        }))
        .collect::<Vec<_>>();
    f.services.queue(wire("openai_chat", "", &calls));
    f.services.queue(wire("openai_chat", "done", &[]));
    let updates = f.turn(false).await.unwrap();
    assert!(!f.dir.join("removed").exists());
    assert_eq!(
        updates
            .iter()
            .filter(|u| matches!(u, Update::ToolFinished { failed: true, .. }))
            .count(),
        4
    );
}

#[tokio::test]
async fn composition_reuses_parent_receipts_and_routes_product_calls_across_protocols() {
    for protocol in ["openai_chat", "openai_responses", "anthropic_messages"] {
        let mut f = Fixture::new(protocol);
        f.context.role = ExecutionRole::Main;
        f.thread
            .config
            .params
            .agent
            .builtin_tools
            .extend(["exec".into(), "wait".into()]);
        f.services.rpc_tools = Some(serde_json::from_str(pot_core::TOOL_SCHEMA).unwrap());
        *f.feedback.authorization.lock().unwrap() = true;
        *f.feedback.permission_decision.lock().unwrap() = Some(true);
        let call = model::ToolCall {
            id: "compose-once".into(),
            name: "exec".into(),
            arguments: json!({"code":"await tools.exec_command({cmd:'printf once >> effect'}); text((await tools.list_tasks({})).managed);","yield_time_ms":1000}),
        };
        f.services
            .queue(wire(protocol, "", std::slice::from_ref(&call)));
        f.services.queue(wire(protocol, "", &[call]));
        f.services.queue(wire(protocol, "done", &[]));
        f.turn(false).await.unwrap();
        assert_eq!(
            std::fs::read_to_string(f.dir.join("effect")).unwrap(),
            "once"
        );
        assert_eq!(f.feedback.permission_requests.lock().unwrap().len(), 1);
        assert_eq!(
            f.services
                .rpc_calls
                .lock()
                .unwrap()
                .iter()
                .filter(|c| c["method"] == "tools/call")
                .count(),
            1
        );
    }
}

struct CountPluginExecutor(Arc<std::sync::atomic::AtomicUsize>, &'static str);
impl ToolExecutor for CountPluginExecutor {
    fn routes_execution_targets(&self) -> bool {
        true
    }
    fn execute<'a>(
        &'a self,
        _: &'a ExecutionContext,
        _: &'a ShellSettings,
        name: &'a str,
        args: &'a Value,
        _: &'a str,
        _: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async move {
            assert_eq!(name, "cua_repl");
            assert_eq!(args["execution_target"], self.1);
            self.0.fetch_add(1, Ordering::SeqCst);
            Ok(json!({"status":"observed"}))
        })
    }
    fn finish<'a>(&'a self, _: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async { Ok(()) })
    }
}

#[tokio::test]
async fn assistant_instruction_updates_always_review_and_are_main_only() {
    let mut f = Fixture::new("openai_chat");
    f.context.role = ExecutionRole::Main;
    let args = json!({"text":"Rules"});
    let request = TurnRequest {
        request_id: "rules".into(),
        text: "Update my rules".into(),
    };
    // Machine authorization availability cannot skip instruction review.
    *f.feedback.authorization.lock().unwrap() = true;
    *f.feedback.permission_decision.lock().unwrap() = Some(false);
    assert!(
        permissions::authorize(
            &f.context,
            &f.thread,
            &request,
            "denied",
            "update_agent_instructions",
            &args
        )
        .await
        .is_err()
    );
    assert_eq!(f.feedback.permission_requests.lock().unwrap().len(), 1);
    *f.feedback.permission_decision.lock().unwrap() = Some(true);
    for call in ["approved-one", "approved-two"] {
        permissions::authorize(
            &f.context,
            &f.thread,
            &request,
            call,
            "update_agent_instructions",
            &args,
        )
        .await
        .unwrap();
    }
    assert_eq!(f.feedback.permission_requests.lock().unwrap().len(), 3);
    permissions::authorize(
        &f.context,
        &f.thread,
        &request,
        "read",
        "read_agent_instructions",
        &json!({}),
    )
    .await
    .unwrap();
    assert_eq!(f.feedback.permission_requests.lock().unwrap().len(), 3);
    for role in [ExecutionRole::Worker, ExecutionRole::Summary] {
        f.context.role = role;
        for tool in ["read_agent_instructions", "update_agent_instructions"] {
            assert!(
                permissions::authorize(&f.context, &f.thread, &request, "forbidden", tool, &args)
                    .await
                    .is_err()
            );
        }
    }
}

#[tokio::test]
async fn main_and_worker_plugin_calls_share_direct_and_composed_permissions() {
    for role in [ExecutionRole::Main, ExecutionRole::Worker] {
        for composed in [false, true] {
            for approved in [false, true] {
                let mut f = Fixture::new("openai_chat");
                f.context.role = role;
                f.services.rpc_tools = Some(vec![]);
                f.thread.config.params.agent.builtin_tools = vec!["exec".into(), "wait".into()];
                *f.feedback.authorization.lock().unwrap() = true;
                let execution_target = if role == ExecutionRole::Main {
                    "host"
                } else {
                    "sandbox:default"
                };
                *f.feedback.prepared_execution_target.lock().unwrap() =
                    Some(execution_target.into());
                *f.feedback.permission_decision.lock().unwrap() = Some(approved);
                let count = Arc::new(std::sync::atomic::AtomicUsize::new(0));
                f.context.tool_executor = Some(Arc::new(CountPluginExecutor(
                    count.clone(),
                    execution_target,
                )));
                let (name, arguments) = if composed {
                    (
                        "exec",
                        json!({"code":"const r = await tools.cua_repl({code:'nodeRepl.write(1)'}); text(r.status);","yield_time_ms":1000}),
                    )
                } else {
                    ("cua_repl", json!({"code":"nodeRepl.write(1)"}))
                };
                f.services.queue(wire(
                    "openai_chat",
                    "",
                    &[model::ToolCall {
                        id: "plugin".into(),
                        name: name.into(),
                        arguments,
                    }],
                ));
                f.services.queue(wire("openai_chat", "done", &[]));
                f.turn(false).await.unwrap();
                assert_eq!(count.load(Ordering::SeqCst), usize::from(approved));
                let requests = f.feedback.permission_requests.lock().unwrap();
                assert_eq!(requests.len(), 1);
                assert_eq!(requests[0]["tool"], "cua_repl");
                assert_eq!(requests[0]["execution_target"], execution_target);
                assert_eq!(
                    requests[0]["call_id"],
                    if composed { "plugin:js:1" } else { "plugin" }
                );
                let schemas = f.services.requests.lock().unwrap()[0]["body"]["tools"].to_string();
                assert!(schemas.contains("cua_repl"));
                let sent = f.services.requests.lock().unwrap();
                let plugin = sent[0]["body"]["tools"]
                    .as_array()
                    .unwrap()
                    .iter()
                    .find(|tool| tool["function"]["name"] == "cua_repl")
                    .unwrap();
                assert!(
                    plugin["function"]["parameters"]["properties"]
                        .get("sandbox_id")
                        .is_none()
                );
                assert!(
                    plugin["function"]["parameters"]["properties"]
                        .get("execution_target")
                        .is_some()
                );
            }
        }
    }
}

#[tokio::test]
async fn thread_switches_between_all_protocols_without_losing_tool_history_or_replaying_effects() {
    for source in PROTOCOLS {
        for target in PROTOCOLS {
            let mut f = Fixture::new(source);
            let call = model::ToolCall {
                id: "effect".into(),
                name: "exec_command".into(),
                arguments: json!({"cmd":"echo once >> effects.txt"}),
            };
            f.services.queue(wire(source, "working", &[call]));
            f.services.queue(wire(source, "completed", &[]));
            f.turn(false).await.unwrap();
            let original = f.feedback.load_context().unwrap().unwrap();
            f.thread.config.set_gateway_protocol(target).unwrap();
            f.thread.config.model = "other-model".into();
            f.thread.config.provider = "other-provider".into();
            f.thread.generation += 1;
            f.services.queue(wire(target, "continued", &[]));
            let request = TurnRequest {
                request_id: "next-turn".into(),
                text: "continue the same goal".into(),
            };
            run(
                &f.context,
                &f.thread,
                &request,
                false,
                &Defaults,
                Some(&f.services),
                &mut |_| Ok(()),
            )
            .await
            .unwrap();
            assert_eq!(
                std::fs::read_to_string(f.context.workspace.join("effects.txt")).unwrap(),
                "once\n"
            );
            let requests = f.services.requests.lock().unwrap();
            let body = requests.last().unwrap()["body"].to_string();
            assert!(body.contains("working"));
            assert!(body.contains("completed"));
            assert!(body.contains("effect"));
            assert!(body.contains("echo once"));
            assert!(!body.contains("thoughtSignature"));
            let saved = f.feedback.load_context().unwrap().unwrap();
            let n = original["messages"].as_array().unwrap().len();
            assert_eq!(
                &saved["messages"].as_array().unwrap()[..n],
                original["messages"].as_array().unwrap()
            );
        }
    }
}

#[tokio::test]
async fn configured_tools_remain_available_without_mutating_thread_config() {
    for role in [ExecutionRole::Main, ExecutionRole::Worker] {
        let mut f = Fixture::new("openai_chat");
        full_loadout(&mut f);
        f.context.role = role;
        let config = f.thread.config.clone();
        f.services.queue(wire("openai_chat", "done", &[]));
        f.turn(false).await.unwrap();
        assert_eq!(f.thread.config, config);
        let requests = f.services.requests.lock().unwrap();
        let tools = model_tools(&requests[0], "openai_chat");
        let names = tools
            .iter()
            .filter_map(|t| t["name"].as_str())
            .collect::<Vec<_>>();
        assert!(names.contains(&"exec_command"));
        assert!(names.contains(&"write_stdin"));
        assert!(names.contains(&"apply_patch"));
        assert!(names.contains(&"cua_repl"));
        if role == ExecutionRole::Main {
            assert!(names.contains(&"create_task"));
        }
    }
}

#[test]
fn stopped_context_reconciliation_pairs_known_results_without_replaying_unknown_calls() {
    let known = model::ToolCall {
        id: "known".into(),
        name: "exec_command".into(),
        arguments: json!({"cmd":"printf X >> once"}),
    };
    let unknown = model::ToolCall {
        id: "unknown".into(),
        name: "exec_command".into(),
        arguments: json!({"cmd":"other"}),
    };
    let mut assistant = Message::text("assistant", String::new());
    assistant.calls = vec![known.clone(), unknown.clone()];
    let context = Context {
        kind: "context".into(),
        version: 2,
        messages: vec![assistant],
        pending: vec![known, unknown],
        started: Some("known".into()),
        last_request: "original".into(),
        receipts: vec![],
        steering_ids: vec![],
        usage_anchor: None,
    };
    let input = serde_json::to_value(context).unwrap();
    let result = json!({"stdout":"X","exit_code":0});
    let receipts = std::collections::BTreeMap::from([("known".into(), result.clone())]);
    let recovered = reconcile_context(&input, &receipts, false).unwrap();
    assert_eq!(recovered["pending"].as_array().unwrap().len(), 1);
    assert_eq!(recovered["pending"][0]["id"], "unknown");
    assert!(recovered["started"].is_null());
    assert_eq!(recovered["last_request"], "original");
    assert_eq!(recovered["messages"][1]["call_id"], "known");
    assert_eq!(
        serde_json::from_str::<Value>(recovered["messages"][1]["text"].as_str().unwrap()).unwrap(),
        result
    );
    assert_eq!(
        reconcile_context(&recovered, &receipts, false).unwrap(),
        recovered
    );
    assert_eq!(input["pending"].as_array().unwrap().len(), 2);
    assert!(reconcile_context(&json!({"type":"context","version":999}), &receipts, false).is_err());
}

#[tokio::test]
async fn execution_checks_current_authority_and_keeps_role_boundaries() {
    let mut f = Fixture::new("openai_chat");
    f.context.role = ExecutionRole::Main;
    *f.feedback.authorization.lock().unwrap() = true;
    *f.feedback.permission_decision.lock().unwrap() = Some(false);
    let turn = TurnRequest {
        request_id: "work".into(),
        text: String::new(),
    };
    for tool in ["exec_command", "write_stdin", "apply_patch"] {
        assert!(
            permissions::authorize(&f.context, &f.thread, &turn, tool, tool, &json!({}))
                .await
                .is_err()
        );
    }
    assert_eq!(f.feedback.permission_requests.lock().unwrap().len(), 3);
    *f.feedback.permission_decision.lock().unwrap() = Some(true);
    f.context.role = ExecutionRole::Worker;
    assert!(
        permissions::authorize(
            &f.context,
            &f.thread,
            &turn,
            "manage",
            "update_agent_instructions",
            &json!({})
        )
        .await
        .is_err()
    );
    f.context.role = ExecutionRole::Summary;
    assert!(
        permissions::authorize(
            &f.context,
            &f.thread,
            &turn,
            "stdin",
            "write_stdin",
            &json!({})
        )
        .await
        .is_err()
    );
}

#[path = "context_tests.rs"]
mod context_tests;
