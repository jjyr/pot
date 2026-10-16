use super::*;
use crate::context_sections;
use model::MessageKind;

fn section(key: &str, text: &str) -> ContextSection {
    ContextSection {
        key: key.into(),
        text: text.into(),
    }
}

async fn send(f: &Fixture, id: &str, text: &str) -> Result<()> {
    run(
        &f.context,
        &f.thread,
        &TurnRequest {
            request_id: id.into(),
            text: text.into(),
        },
        false,
        &Defaults,
        Some(&f.services),
        &mut |_| Ok(()),
    )
    .await
}

fn input(body: &Value, protocol: &str) -> Vec<Value> {
    body[match protocol {
        "openai_chat" | "anthropic_messages" => "messages",
        "openai_responses" => "input",
        "gemini" => "contents",
        _ => unreachable!(),
    }]
    .as_array()
    .unwrap()
    .clone()
}

#[test]
fn sections_append_replacements_revocations_and_recover_baseline_from_retained_history() {
    let mut messages = vec![];
    let initial = vec![
        section("tasks", "task-a: running"),
        section("instructions", "Write PDF"),
        section("models", "model-a"),
    ];
    context_sections::synchronize(&mut messages, &initial).unwrap();
    messages.push(Message::text("user", "Compare the companies".into()));
    let prefix = serde_json::to_value(&messages).unwrap();
    // A fresh invocation reconstructs the baseline from classified retained items.
    messages = serde_json::from_value(prefix.clone()).unwrap();
    let mut reordered = initial.clone();
    reordered.reverse();
    context_sections::synchronize(&mut messages, &reordered).unwrap();
    assert_eq!(serde_json::to_value(&messages).unwrap(), prefix);
    context_sections::synchronize(
        &mut messages,
        &[
            section("instructions", "Write Markdown"),
            section("models", "model-a"),
        ],
    )
    .unwrap();
    assert_eq!(serde_json::to_value(&messages[..4]).unwrap(), prefix);
    assert_eq!(messages.len(), 6);
    assert!(messages[4].text.contains("Write Markdown"));
    assert!(messages[4].text.contains("replaces all earlier contents"));
    assert!(messages[5].text.contains("no longer apply"));
    let replaced = serde_json::to_value(&messages).unwrap();
    context_sections::synchronize(
        &mut messages,
        &[
            section("instructions", "Write Markdown"),
            section("models", "model-a"),
        ],
    )
    .unwrap();
    assert_eq!(serde_json::to_value(&messages).unwrap(), replaced);
    // A real reset removes the old fragments: the current state must be emitted again.
    messages.clear();
    context_sections::synchronize(&mut messages, &initial).unwrap();
    assert_eq!(messages.len(), 3);
    // Neither context nor summaries count as real user boundaries.
    assert!(messages.iter().all(|m| !context_sections::is_user(m)));
    let mut summary = Message::text("user", "summary".into());
    summary.kind = MessageKind::Summary;
    assert!(!context_sections::is_user(&summary));
}

#[test]
fn invalid_section_sets_do_not_partially_mutate_history() {
    for sections in [
        vec![section("tasks", "one"), section("tasks", "two")],
        vec![section("bad\"key", "data")],
    ] {
        let mut messages = vec![Message::text("user", "original".into())];
        let original = serde_json::to_value(&messages).unwrap();
        assert!(context_sections::synchronize(&mut messages, &sections).is_err());
        assert_eq!(serde_json::to_value(&messages).unwrap(), original);
    }
}

#[test]
fn line_endings_and_outer_whitespace_do_not_append_duplicate_context() {
    let mut messages =
        context_sections::full(&[section("instructions", "Line one\r\nLine two\r\n")]).unwrap();
    let prefix = serde_json::to_value(&messages).unwrap();
    context_sections::synchronize(
        &mut messages,
        &[section("instructions", "\nLine one\nLine two\n")],
    )
    .unwrap();
    assert_eq!(serde_json::to_value(messages).unwrap(), prefix);
}

#[tokio::test]
async fn text_only_request_projection_does_not_rewrite_retained_user_attachments() {
    let mut f = Fixture::new("openai_chat");
    f.thread.config.params.agent.plugins.clear();
    f.thread.config.params.agent.model_catalog = Some(
        serde_json::from_value(json!({"schema_version":1,"default":{"input_modalities":["text"]}}))
            .unwrap(),
    );
    let mut user = Message::text("user", "Prior question".into());
    user.images.push(model::ModelImage {
        mime_type: "image/png".into(),
        data: "AQID".into(),
        width: 1,
        height: 1,
    });
    let state = context(vec![
        user,
        Message::text("assistant", "Earlier answer".into()),
    ]);
    let original = serde_json::to_value(&state.messages).unwrap();
    state.save(&f.context).unwrap();
    f.services.queue(wire("openai_chat", "Answer", &[]));
    send(&f, "next", "Continue").await.unwrap();
    let saved: Context =
        serde_json::from_value(f.feedback.load_context().unwrap().unwrap()).unwrap();
    assert_eq!(
        serde_json::to_value(&saved.messages[..2]).unwrap(),
        original
    );
    assert!(
        !f.services.requests.lock().unwrap()[0]["body"]
            .to_string()
            .contains("AQID")
    );
}

#[tokio::test]
async fn context_sections_do_not_count_as_additional_conversation_turns_for_compaction() {
    let mut f = Fixture::new("openai_chat");
    f.thread.config.params.agent.auto_compact = true;
    f.thread.config.params.agent.compact_at = Some(1);
    let sections = vec![
        section("instructions", "Rules"),
        section("models", "Models"),
        section("tasks", "Tasks"),
    ];
    let mut messages = context_sections::full(&sections).unwrap();
    messages.extend([
        Message::text("user", "First".into()),
        Message::text("assistant", "Answer".into()),
        Message::text("user", "Second".into()),
    ]);
    let mut state = context(messages);
    let original = serde_json::to_value(&state).unwrap();
    compact(
        Some(&f.services),
        &f.context,
        &f.thread,
        &mut state,
        &mut |_| Ok(()),
        &Defaults,
        "base",
        &[],
        &sections,
    )
    .await
    .unwrap();
    assert!(f.services.requests.lock().unwrap().is_empty());
    assert_eq!(serde_json::to_value(state).unwrap(), original);
}

#[tokio::test]
async fn provider_cache_expiry_and_local_calibration_expiry_preserve_the_encoded_prefix() {
    for protocol in PROTOCOLS {
        let mut f = Fixture::new(protocol);
        f.context.instructions = "Use Chinese; preserve completed actions".into();
        f.context.context_sections = vec![
            section("models", "model-a"),
            section("tasks", "task-a: running"),
        ];
        f.services.queue(wire(protocol, "first answer", &[]));
        send(&f, "first", "First real user input").await.unwrap();
        let first_saved = f.feedback.load_context().unwrap().unwrap();
        let first_body = f.services.requests.lock().unwrap()[0]["body"].clone();

        // Simulate provider KV-cache expiry: a later response reports zero cache reads.
        // No client TTL can promise server eviction; this verifies our replay contract.
        let mut cold = wire(protocol, "cold-cache answer", &[]);
        for frame in &mut cold {
            match protocol {
                "openai_chat" if frame.get("usage").is_some() => {
                    frame["usage"]["prompt_tokens_details"]["cached_tokens"] = json!(0)
                }
                "openai_responses" => {
                    frame["response"]["usage"]["input_tokens_details"]["cached_tokens"] = json!(0)
                }
                "anthropic_messages" if frame["type"] == "message_start" => {
                    frame["message"]["usage"]["cache_read_input_tokens"] = json!(0)
                }
                "gemini" => frame["usageMetadata"]["cachedContentTokenCount"] = json!(0),
                _ => {}
            }
        }
        // Reopen the serialized checkpoint without the optional usage estimate cache.
        let mut reopened = first_saved.clone();
        reopened["usage_anchor"] = Value::Null;
        f.feedback.save_context(&reopened).unwrap();
        f.services.queue(cold);
        send(&f, "second", "Second real user input").await.unwrap();
        let requests = f.services.requests.lock().unwrap();
        let second_body = &requests[1]["body"];
        let prefix = input(&first_body, protocol);
        assert_eq!(
            &input(second_body, protocol)[..prefix.len()],
            prefix.as_slice(),
            "{protocol}"
        );
        let old = first_saved["messages"].as_array().unwrap();
        let saved = f.feedback.load_context().unwrap().unwrap();
        assert_eq!(
            &saved["messages"].as_array().unwrap()[..old.len()],
            old,
            "{protocol}"
        );
        let state: Context = serde_json::from_value(saved).unwrap();
        assert_eq!(
            state
                .messages
                .iter()
                .filter(|m| matches!(&m.kind, MessageKind::Context {key,..} if key=="instructions"))
                .count(),
            1
        );
        assert_eq!(
            state
                .messages
                .iter()
                .filter(|m| context_sections::is_user(m))
                .map(|m| m.text.as_str())
                .collect::<Vec<_>>(),
            ["First real user input", "Second real user input"]
        );
        assert!(
            f.feedback
                .observations
                .lock()
                .unwrap()
                .iter()
                .any(|e| matches!(
                    e,
                    ExecutionObservation::UsageObserved {
                        cache_read_tokens: Some(0),
                        ..
                    }
                )),
            "{protocol}"
        );
        assert!(!second_body.to_string().contains("fingerprint"));
    }
}

#[tokio::test]
async fn changed_sections_append_after_old_conversation_without_rewriting_system_or_tools() {
    for protocol in PROTOCOLS {
        let mut f = Fixture::new(protocol);
        f.context.instructions = "Write PDF".into();
        f.context.context_sections = vec![section("models", "model-a")];
        f.services.queue(wire(protocol, "done", &[]));
        send(&f, "first", "First input").await.unwrap();
        let before = f.feedback.load_context().unwrap().unwrap();
        let first = f.services.requests.lock().unwrap()[0]["body"].clone();
        f.context.instructions = "Write Markdown".into();
        f.context.context_sections.clear();
        f.services.queue(wire(protocol, "done", &[]));
        send(&f, "second", "Continue").await.unwrap();
        let saved = f.feedback.load_context().unwrap().unwrap();
        let old = before["messages"].as_array().unwrap();
        assert_eq!(&saved["messages"].as_array().unwrap()[..old.len()], old);
        let state: Context = serde_json::from_value(saved).unwrap();
        assert!(state.messages[old.len()].text.contains("Write Markdown"));
        assert!(
            state.messages[old.len() + 1]
                .text
                .contains("no longer apply")
        );
        assert_eq!(state.messages[old.len() + 2].text, "Continue");
        let requests = f.services.requests.lock().unwrap();
        let second = &requests[1]["body"];
        let prefix = input(&first, protocol);
        assert_eq!(
            &input(second, protocol)[..prefix.len()],
            prefix.as_slice(),
            "{protocol}"
        );
        for key in ["instructions", "system", "systemInstruction", "tools"] {
            assert_eq!(first.get(key), second.get(key), "{protocol}:{key}");
        }
    }
}

#[tokio::test]
async fn compaction_rebuilds_injections_summary_last_user_and_resets_the_baseline_atomically() {
    for protocol in PROTOCOLS {
        let mut f = Fixture::new(protocol);
        f.thread.config.params.agent.auto_compact = true;
        f.thread.config.params.agent.compact_at = Some(1);
        let sections = vec![
            section("instructions", "Current rules"),
            section("models", "current-model"),
        ];
        let mut messages = context_sections::full(&[section("models", "obsolete-model")]).unwrap();
        messages.push(Message::text("user", "Old question".into()));
        messages.push(Message::text("assistant", "old response ".repeat(200)));
        let mut last_user = Message::text("user", "Compare both companies".into());
        last_user.images.push(model::ModelImage {
            mime_type: "image/png".into(),
            data: "AQID".into(),
            width: 1,
            height: 1,
        });
        messages.push(last_user.clone());
        let mut assistant = Message::text("assistant", String::new());
        assistant.calls = vec![call("query")];
        messages.push(assistant);
        let mut result = Message::text(
            "tool",
            "B revenue=12; file=report.md; effect completed".into(),
        );
        result.call_id = "query".into();
        messages.push(result);
        let mut state = context(messages);
        f.services.queue(wire(
            protocol,
            "B revenue=12; report.md exists. Do not repeat the completed action.",
            &[],
        ));
        compact(
            Some(&f.services),
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Ok(()),
            &Estimate(100),
            "base",
            &[],
            &sections,
        )
        .await
        .unwrap();
        assert_eq!(state.messages.len(), 4);
        assert!(matches!(
            state.messages[0].kind,
            MessageKind::Context { .. }
        ));
        assert!(matches!(
            state.messages[1].kind,
            MessageKind::Context { .. }
        ));
        assert_eq!(state.messages[2].kind, MessageKind::Summary);
        assert_eq!(
            serde_json::to_value(&state.messages[3]).unwrap(),
            serde_json::to_value(last_user).unwrap()
        );
        assert!(
            state
                .messages
                .iter()
                .all(|m| m.calls.is_empty() && m.role != "tool")
        );
        let summary_request = &f.services.requests.lock().unwrap()[0]["body"];
        assert!(summary_request.to_string().contains("effect completed"));
        assert!(!summary_request.to_string().contains("obsolete-model"));
        let installed = f.feedback.load_context().unwrap().unwrap();
        assert_eq!(installed, serde_json::to_value(&state).unwrap());
        let prefix = serde_json::to_value(&state.messages).unwrap();
        let mut reopened: Context = serde_json::from_value(installed).unwrap();
        context_sections::synchronize(&mut reopened.messages, &sections).unwrap();
        assert_eq!(serde_json::to_value(&reopened.messages).unwrap(), prefix);
        let encoded = model::request(
            &Protocol::from_name(protocol).unwrap(),
            "fixture",
            "base",
            &reopened.messages,
            &[],
            128,
            None,
        )
        .unwrap()
        .to_string();
        assert!(
            encoded.find("Current rules").unwrap()
                < encoded.find("Previous context summary").unwrap()
        );
        assert!(
            encoded.find("Previous context summary").unwrap()
                < encoded.find("Compare both companies").unwrap()
        );
    }
}

#[tokio::test]
async fn failed_compaction_checkpoint_preserves_old_sections_and_history() {
    let mut f = Fixture::new("openai_chat");
    f.thread.config.params.agent.auto_compact = true;
    f.thread.config.params.agent.compact_at = Some(1);
    let mut state = compact_history();
    let mut messages = context_sections::full(&[section("instructions", "Old rules")]).unwrap();
    messages.append(&mut state.messages);
    state.messages = messages;
    state.save(&f.context).unwrap();
    let original = serde_json::to_value(&state).unwrap();
    *f.feedback.fail_save.lock().unwrap() = Some(SaveFault::Any);
    f.services.queue(wire("openai_chat", "summary", &[]));
    assert!(
        compact(
            Some(&f.services),
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Ok(()),
            &Estimate(100),
            "base",
            &[],
            &[section("instructions", "New rules")]
        )
        .await
        .is_err()
    );
    assert_eq!(serde_json::to_value(state).unwrap(), original);
    assert_eq!(f.feedback.load_context().unwrap().unwrap(), original);
}

#[tokio::test]
async fn oversized_summary_request_is_rejected_before_transport_without_replacing_history() {
    let mut f = Fixture::new("openai_chat");
    f.thread.config.params.agent.auto_compact = true;
    f.thread.config.params.agent.compact_at = Some(1);
    f.thread.config.params.agent.max_input_tokens = Some(64);
    let mut state = compact_history();
    state.save(&f.context).unwrap();
    let original = serde_json::to_value(&state).unwrap();
    let error = compact(
        Some(&f.services),
        &f.context,
        &f.thread,
        &mut state,
        &mut |_| Ok(()),
        &Defaults,
        "base",
        &[],
        &[],
    )
    .await
    .unwrap_err();
    assert!(error.to_string().contains("Compaction request exceeds"));
    assert!(f.services.requests.lock().unwrap().is_empty());
    assert_eq!(serde_json::to_value(state).unwrap(), original);
    assert_eq!(f.feedback.load_context().unwrap().unwrap(), original);
}

#[tokio::test]
async fn result_summary_execution_does_not_revoke_the_main_assistants_sections() {
    let mut f = Fixture::new("openai_chat");
    f.context.role = ExecutionRole::Summary;
    f.services.rpc_tools = Some(vec![]);
    let mut messages = context_sections::full(&[
        section("instructions", "Main rules"),
        section("models", "model-a"),
    ])
    .unwrap();
    messages.push(Message::text("user", "Original goal".into()));
    messages.push(Message::text("assistant", "Working".into()));
    let state = context(messages);
    state.save(&f.context).unwrap();
    let prefix = serde_json::to_value(&state.messages).unwrap();
    f.services.queue(wire("openai_chat", "Result summary", &[]));
    send(&f, "result-notification", "Task completed: report.md")
        .await
        .unwrap();
    let saved: Context =
        serde_json::from_value(f.feedback.load_context().unwrap().unwrap()).unwrap();
    assert_eq!(serde_json::to_value(&saved.messages[..4]).unwrap(), prefix);
    assert_eq!(
        saved
            .messages
            .iter()
            .filter(|m| matches!(m.kind, MessageKind::Context { .. }))
            .count(),
        2
    );
    assert!(
        saved
            .messages
            .iter()
            .all(|m| !m.text.contains("no longer apply"))
    );
    assert_eq!(
        saved
            .messages
            .iter()
            .rev()
            .find(|m| context_sections::is_user(m))
            .unwrap()
            .text,
        "Original goal"
    );
    assert!(
        saved
            .messages
            .iter()
            .any(|m| m.kind == MessageKind::Notification)
    );
}

#[tokio::test]
async fn long_single_turn_compacts_tools_and_can_continue_without_repeating_injections() {
    for protocol in PROTOCOLS {
        let mut f = Fixture::new(protocol);
        f.thread.config.params.agent.auto_compact = true;
        f.thread.config.params.agent.compact_at = Some(1);
        f.thread.config.params.agent.plugins.clear();
        f.context.instructions = "Current rules".into();
        f.context.context_sections = vec![section("models", "model-a")];
        let sections = vec![
            section("instructions", "Current rules"),
            section("models", "model-a"),
        ];
        let mut messages = context_sections::full(&sections).unwrap();
        messages.push(Message::text("user", "Create report.md".into()));
        let mut assistant = Message::text("assistant", String::new());
        assistant.calls = vec![call("effect")];
        messages.push(assistant);
        let mut output = Message::text("tool", "Created report.md successfully. ".repeat(200));
        output.call_id = "effect".into();
        messages.push(output);
        let mut state = context(messages);
        f.services.queue(wire(
            protocol,
            "report.md was created; deliver the existing file without rerunning the write.",
            &[],
        ));
        compact(
            Some(&f.services),
            &f.context,
            &f.thread,
            &mut state,
            &mut |_| Ok(()),
            &Defaults,
            "base",
            &[],
            &sections,
        )
        .await
        .unwrap();
        assert_eq!(state.messages.len(), 4);
        assert!(state.messages[2].text.contains("without rerunning"));
        assert_eq!(state.messages[3].text, "Create report.md");
        let prefix = serde_json::to_value(&state.messages).unwrap();
        f.thread.config.params.agent.auto_compact = false;
        f.services.queue(wire(protocol, "Here is report.md", &[]));
        send(&f, "continuation", "Deliver the file").await.unwrap();
        let installed: Context =
            serde_json::from_value(f.feedback.load_context().unwrap().unwrap()).unwrap();
        assert_eq!(
            serde_json::to_value(&installed.messages[..4]).unwrap(),
            prefix
        );
        assert_eq!(
            installed
                .messages
                .iter()
                .filter(|m| matches!(m.kind, MessageKind::Context { .. }))
                .count(),
            2
        );
        assert!(!f.dir.join("effects.txt").exists());
    }
}
