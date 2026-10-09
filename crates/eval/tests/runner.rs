use pot_core::*;
use pot_eval::*;
use serde_json::{Value, json};
use std::{collections::BTreeMap, path::PathBuf};
struct Temp(PathBuf);
impl Temp {
    fn new() -> Self {
        let p = std::env::temp_dir().join(format!("pot-eval-{}", uuid::Uuid::new_v4()));
        std::fs::create_dir(&p).unwrap();
        Self(p)
    }
}
impl Drop for Temp {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}
struct Config;
impl ProfileConfig for Config {
    fn model(&self) -> &str {
        "fixture"
    }
    fn max_tokens(&self) -> u64 {
        512
    }
}
fn scenario(protocol: &str, responses: Vec<ScriptedResponse>) -> Scenario {
    let mut profile = Profile::api(&Config);
    profile.params.agent.system_prompt = Some("Complete the task.".into());
    Scenario {
        id: "contract-v1".into(),
        profile,
        protocol: protocol.into(),
        turns: vec!["Complete the task.".into()],
        responses,
        fixtures: BTreeMap::new(),
        expected_files: BTreeMap::new(),
        expected_final_text: Some("done".into()),
        turn_assertions: vec![],
        deadline_ms: 10000,
    }
}
fn response(protocol: &str, calls: Vec<Value>, text: &str) -> ScriptedResponse {
    ScriptedResponse {
        stage: "agent".into(),
        frames: wire(protocol, calls, text),
        body_contains: vec![],
    }
}
fn wire(protocol: &str, calls: Vec<Value>, text: &str) -> Vec<Value> {
    match protocol {
        "openai_chat" => vec![
            json!({"choices":[{"delta":{"content":text,"tool_calls":calls.iter().enumerate().map(|(i,c)|json!({"index":i,"id":c["id"],"type":"function","function":{"name":c["name"],"arguments":c["arguments"].to_string()}})).collect::<Vec<_>>()},"finish_reason":null}]}),
            json!({"choices":[{"delta":{},"finish_reason":if calls.is_empty(){"stop"}else{"tool_calls"}}]}),
            json!({"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5}}),
        ],
        "openai_responses" => {
            let mut output = vec![
                json!({"id":"reason","type":"reasoning","summary":[],"encrypted_content":"opaque-reasoning"}),
            ];
            if !text.is_empty() {
                output.push(json!({"id":"message","type":"message","role":"assistant","content":[{"type":"output_text","text":text}]}));
            }
            output.extend(calls.iter().map(|c|json!({"id":format!("item-{}",c["id"]),"type":"function_call","call_id":c["id"],"name":c["name"],"arguments":c["arguments"].to_string()})));
            vec![
                json!({"type":"response.output_text.delta","delta":text}),
                json!({"type":"response.completed","response":{"status":"completed","output":output,"usage":{"input_tokens":10,"output_tokens":5}}}),
            ]
        }
        "anthropic_messages" => {
            let mut events = vec![
                json!({"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}),
                json!({"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":"","signature":""}}),
                json!({"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"brief"}}),
                json!({"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}),
                json!({"type":"content_block_stop","index":0}),
            ];
            events.extend([json!({"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}),json!({"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":text}}),json!({"type":"content_block_stop","index":1})]);
            for (i, c) in calls.iter().enumerate() {
                events.extend([json!({"type":"content_block_start","index":i+2,"content_block":{"type":"tool_use","id":c["id"],"name":c["name"],"input":{}}}),json!({"type":"content_block_delta","index":i+2,"delta":{"type":"input_json_delta","partial_json":c["arguments"].to_string()}}),json!({"type":"content_block_stop","index":i+2})]);
            }
            events.extend([json!({"type":"message_delta","delta":{"stop_reason":if calls.is_empty(){"end_turn"}else{"tool_use"}},"usage":{"output_tokens":5}}),json!({"type":"message_stop"})]);
            events
        }
        "gemini" => {
            let mut parts = vec![];
            if !text.is_empty() {
                parts.push(json!({"text":text}));
            }
            parts.extend(calls.iter().map(|c|json!({"functionCall":{"id":c["id"],"name":c["name"],"args":c["arguments"]},"thoughtSignature":"opaque-thought"})));
            vec![
                json!({"candidates":[{"content":{"role":"model","parts":parts},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}),
            ]
        }
        _ => panic!(),
    }
}
#[tokio::test]
async fn four_protocols_execute_real_tools_and_count_requests_once() {
    for protocol in [
        "openai_chat",
        "openai_responses",
        "anthropic_messages",
        "gemini",
    ] {
        let dir = Temp::new();
        let calls = vec![
            json!({"id":"exec","name":"exec_command","arguments":{"cmd":"printf APRICOT > result.txt"}}),
        ];
        let mut case = scenario(
            protocol,
            vec![
                response(protocol, calls, "working"),
                response(protocol, vec![], "done"),
            ],
        );
        case.expected_files
            .insert("result.txt".into(), "APRICOT".into());
        case.responses[1].body_contains.push("exit_code".into());
        let report = run_scripted(&case, &dir.0.join("run")).await.unwrap();
        assert!(report.success, "{}: {:?}", protocol, report.errors);
        assert!(report.measurement_complete);
        assert_eq!(report.tool_count, 1);
        assert_eq!(report.request_count, 2);
        assert_eq!(report.usage.input_tokens, Some(20));
        assert_eq!(report.usage.output_tokens, Some(10));
        assert_eq!(validate(&dir.0.join("run")).unwrap().usage, report.usage);
        assert!(run_scripted(&case, &dir.0.join("run")).await.is_err());
    }
}
#[tokio::test]
async fn repeated_compaction_is_attributed_and_preserves_continuation() {
    let dir = Temp::new();
    let p = "openai_chat";
    let mut summary = response(p, vec![], "Remember APRICOT");
    summary.stage = "compaction".into();
    let mut case = scenario(
        p,
        vec![
            response(p, vec![], &"large ".repeat(300)),
            response(p, vec![], &"large ".repeat(300)),
            summary.clone(),
            response(p, vec![], &"large ".repeat(300)),
            summary,
            response(p, vec![], "done"),
        ],
    );
    case.turns = vec![
        "First APRICOT".into(),
        "Second".into(),
        "Third".into(),
        "Recall APRICOT".into(),
    ];
    case.profile.params.agent.compact_at = Some(1);
    case.responses[3]
        .body_contains
        .push("Remember APRICOT".into());
    case.responses[5]
        .body_contains
        .push("Remember APRICOT".into());
    let report = run_scripted(&case, &dir.0.join("run")).await.unwrap();
    assert!(report.success, "{:?}", report.errors);
    assert_eq!(report.compaction_count, 2);
    assert_eq!(report.stage_usage["compaction"].input_tokens, Some(20));
    assert_eq!(report.stage_usage["agent"].input_tokens, Some(40));
    assert_eq!(report.usage.input_tokens, Some(60));
}
#[tokio::test]
async fn missing_usage_failure_and_unused_trace_are_not_efficiency_results() {
    let dir = Temp::new();
    let p = "openai_chat";
    let mut response = response(p, vec![], "done");
    response.frames.pop();
    let case = scenario(p, vec![response]);
    let report = run_scripted(&case, &dir.0.join("unknown")).await.unwrap();
    assert!(report.success);
    assert!(!report.measurement_complete);
    assert_eq!(report.usage.input_tokens, None);
    assert_eq!(compare(&report, &report).unwrap()["comparable"], false);
    let mut case = case.clone();
    case.responses[0].frames = vec![json!({"error":{"message":"failure"}})];
    let failed = run_scripted(&case, &dir.0.join("failed")).await.unwrap();
    assert!(!failed.success);
    assert!(!failed.measurement_complete);
    let mut case = scenario(p, vec![response_for_unused()]);
    case.responses.push(response_for_unused());
    assert!(
        !run_scripted(&case, &dir.0.join("unused"))
            .await
            .unwrap()
            .success
    );
}
fn response_for_unused() -> ScriptedResponse {
    response("openai_chat", vec![], "done")
}
#[tokio::test]
async fn tampered_records_and_escape_paths_are_rejected() {
    let dir = Temp::new();
    let mut case = scenario("openai_chat", vec![response("openai_chat", vec![], "done")]);
    case.fixtures.insert("../escape".into(), "bad".into());
    assert!(run_scripted(&case, &dir.0.join("escape")).await.is_err());
    assert!(!dir.0.join("escape/escape").exists());
    case.fixtures.clear();
    run_scripted(&case, &dir.0.join("valid")).await.unwrap();
    let path = dir.0.join("valid/events.jsonl");
    let mut events = std::fs::read_to_string(&path).unwrap();
    events.push_str(&events.clone());
    std::fs::write(path, events).unwrap();
    assert!(validate(&dir.0.join("valid")).is_err());
}
#[tokio::test]
async fn failed_artifact_keeps_its_usage_and_invalidates_comparison() {
    let dir = Temp::new();
    let mut case = scenario("openai_chat", vec![response("openai_chat", vec![], "done")]);
    case.expected_files
        .insert("missing.txt".into(), "expected".into());
    let report = run_scripted(&case, &dir.0.join("run")).await.unwrap();
    assert!(!report.success);
    assert!(report.measurement_complete);
    assert_eq!(report.usage.input_tokens, Some(10));
    assert_eq!(
        compare(&report, &report).unwrap()["input_delta"],
        Value::Null
    );
}

#[tokio::test]
async fn tool_batches_patch_and_large_output_use_the_configured_limits() {
    let dir = Temp::new();
    let p = "openai_chat";
    let calls = vec![
        json!({"id":"patch","name":"apply_patch","arguments":{"patch":"*** Begin Patch\n*** Add File: patched.txt\n+APRICOT\n*** End Patch"}}),
        json!({"id":"large","name":"exec_command","arguments":{"cmd":"printf '%0200d' 0","max_output_chars":32}}),
    ];
    let mut case = scenario(
        p,
        vec![response(p, calls, "working"), response(p, vec![], "done")],
    );
    case.expected_files
        .insert("patched.txt".into(), "APRICOT\n".into());
    let report = run_scripted(&case, &dir.0.join("run")).await.unwrap();
    assert!(report.success, "{:?}", report.errors);
    assert_eq!(report.tool_count, 2);
    let requests: Value =
        serde_json::from_slice(&std::fs::read(dir.0.join("run/requests.json")).unwrap()).unwrap();
    let body = requests[1]["body"].to_string();
    assert!(body.contains("truncated"));
    assert!(!body.contains(&"0".repeat(200)));
}
#[tokio::test]
async fn deadline_cancels_a_real_process_and_retains_failed_run() {
    let dir = Temp::new();
    let p = "openai_chat";
    let calls = vec![
        json!({"id":"slow","name":"exec_command","arguments":{"cmd":"echo $$ > pid; sleep 30"}}),
    ];
    let mut case = scenario(p, vec![response(p, calls, "working")]);
    case.deadline_ms = 200;
    let report = run_scripted(&case, &dir.0.join("run")).await.unwrap();
    assert!(!report.success);
    assert!(report.errors.iter().any(|e| e.contains("deadline")));
    let pid = std::fs::read_to_string(dir.0.join("run/workspace/pid")).unwrap();
    let status = std::process::Command::new("kill")
        .args(["-0", pid.trim()])
        .output()
        .unwrap()
        .status;
    assert!(!status.success(), "Timed-out process still alive");
}
#[tokio::test]
async fn zero_usage_and_usage_on_failed_response_are_preserved() {
    let dir = Temp::new();
    let p = "openai_chat";
    let mut zero = response(p, vec![], "done");
    zero.frames.last_mut().unwrap()["usage"] = json!({"prompt_tokens":0,"completion_tokens":0});
    let report = run_scripted(&scenario(p, vec![zero]), &dir.0.join("zero"))
        .await
        .unwrap();
    assert!(report.measurement_complete);
    assert_eq!(report.usage.input_tokens, Some(0));
    let failed = ScriptedResponse {
        stage: "agent".into(),
        body_contains: vec![],
        frames: vec![
            json!({"type":"response.failed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}),
        ],
    };
    let report = run_scripted(
        &scenario("openai_responses", vec![failed]),
        &dir.0.join("failed"),
    )
    .await
    .unwrap();
    assert!(!report.success);
    assert_eq!(report.usage.input_tokens, Some(10));
}

#[tokio::test]
async fn cache_and_reasoning_subsets_are_recorded_without_extra_totals() {
    let dir = Temp::new();
    let p = "openai_chat";
    let mut sample = response(p, vec![], "done");
    sample.frames.last_mut().unwrap()["usage"] = json!({"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":60},"completion_tokens_details":{"reasoning_tokens":5}});
    let report = run_scripted(&scenario(p, vec![sample]), &dir.0.join("run"))
        .await
        .unwrap();
    assert_eq!(report.usage.input_tokens, Some(100));
    assert_eq!(report.usage.output_tokens, Some(20));
    assert_eq!(report.usage.cache_read_tokens, Some(60));
    assert_eq!(report.usage.reasoning_tokens, Some(5));
    assert_eq!(report.usage.cache_write_tokens, None);
    let report = run_scripted(
        &scenario(
            "anthropic_messages",
            vec![response("anthropic_messages", vec![], "done")],
        ),
        &dir.0.join("missing"),
    )
    .await
    .unwrap();
    assert_eq!(report.usage.cache_read_tokens, None);
}
