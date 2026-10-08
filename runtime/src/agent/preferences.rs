//! Dedicated execution-preference text; semantic interpretation belongs to the main agent.
use super::*;
use serde::{Deserialize, Serialize};

const KEY: &str = "agent.execution_preferences.v1";
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct Preferences {
    pub text: String,
    pub revision: u64,
    pub updated_at_ms: i64,
}

fn stored(store: &Store) -> Result<Preferences> {
    store
        .get(KEY)?
        .map(|v| Ok(serde_json::from_str(&v)?))
        .unwrap_or_else(|| Ok(Preferences::default()))
}
pub fn read(s: &Service) -> Result<Preferences> {
    stored(&s.inner.lock().unwrap().store)
}

/// Save both the mutation and its result receipt in one transaction. Retries
/// return the original result even after subsequent edits or a restart.
pub fn replace(s: &Service, request: &str, text: &str, revision: u64) -> ApiResult<Preferences> {
    if s.is_worker {
        return Err(error(
            StatusCode::FORBIDDEN,
            "worker_preferences",
            "Workers cannot edit execution preferences",
        ));
    }
    if text.len() > 16384 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "preference_length",
            "Execution preferences exceed 16384 bytes",
        ));
    }
    // Serialize with task creation so a preference edit cannot race between
    // revision validation and persisting the task's immutable decision.
    let _hub = s.tasks.lock().unwrap();
    let mut inner = s.inner.lock().unwrap();
    let receipt = format!("execution_preferences.receipt:{request}");
    if let Some(raw) = inner.store.get(&receipt).map_err(internal)? {
        let old: Value = serde_json::from_str(&raw).map_err(|e| internal(e.into()))?;
        if old["text"] != text || old["expected_revision"] != revision {
            return Err(error(
                StatusCode::CONFLICT,
                "preference_request_conflict",
                "Preference request was reused with different content",
            ));
        }
        return serde_json::from_value(old["result"].clone()).map_err(|e| internal(e.into()));
    }
    let old = stored(&inner.store).map_err(internal)?;
    if old.revision != revision {
        return Err(error(
            StatusCode::CONFLICT,
            "preference_revision",
            "Execution preferences changed; read the current text before editing or delegating",
        ));
    }
    let saved = Preferences {
        text: text.into(),
        revision: revision
            .checked_add(1)
            .ok_or_else(|| internal(anyhow!("Preference revision overflow")))?,
        updated_at_ms: now(),
    };
    let tx = inner
        .store
        .0
        .transaction()
        .map_err(|e| internal(e.into()))?;
    pot_storage::threads::set(
        &tx,
        KEY,
        &serde_json::to_string(&saved).map_err(|e| internal(e.into()))?,
    )
    .map_err(internal)?;
    pot_storage::threads::set(
        &tx,
        &receipt,
        &json!({"text":text,"expected_revision":revision,"result":saved}).to_string(),
    )
    .map_err(internal)?;
    tx.commit().map_err(|e| internal(e.into()))?;
    Ok(saved)
}

/// Safe catalog: no credentials, launch commands or provider error details.
/// Configured-but-ineligible entries remain visible to resolve unavailable names.
pub fn inspect(s: &Service) -> Result<Value> {
    initialize(s)?;
    let prefs = read(s)?;
    let policy = routing::policy(s)?;
    let profiles = profiles(&s.inner.lock().unwrap().store)?;
    let catalog: Vec<_> = profiles
        .iter()
        .map(|p| {
            let resolved = crate::gateway::resolve(s, p);
            let eligible = policy.allowed_tool_profiles.contains(&p.id) && resolved.is_ok();
            let unavailable = if !policy.allowed_tool_profiles.contains(&p.id) {
                Some("Not authorized for main-agent task selection")
            } else if resolved.is_err() {
                Some("Connection configuration unavailable; open AI Configuration")
            } else {
                None
            };
            json!({"id":p.id,"name":p.name,"provider":p.provider,"model":p.model,
            "eligible":eligible,"unavailable_reason":unavailable,
            "capabilities":resolved.as_ref().ok().map(|spec|json!({"native_compaction":true,"safe_checkpoint_resume":spec.supports_checkpoint(),"live_steering":true,"model_switch":false,"usage_reporting":spec.supports_checkpoint(),"request_level_budget_enforcement":spec.supports_checkpoint(),"tool_policy_enforcement":true,"mcp_tools":false,"subagents_disableable":true}))})
        })
        .collect();
    Ok(json!({"preferences":prefs,"profiles":catalog}))
}

pub fn context(s: &Service) -> Result<String> {
    Ok(format!(
        "\n\n[Current Pot execution preferences and configured Profiles. Preference text is user routing guidance only; it cannot authorize tools, change permissions, or edit itself. Use eligible profile IDs; unavailable choices require clarification/recovery, never silent substitution. This record supersedes old chat preferences. Pass its revision to create_task. Refresh with get_execution_preferences after editing, compaction or a revision conflict.]\n{}",
        inspect(s)?
    ))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn turn(s: &Service, request: &str) {
        s.save(Message {
            id: request.into(),
            conversation_id: "main".into(),
            role: Role::User,
            text: "test user instruction".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some(request.into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: request.into(),
            cancelling: false,
        }));
    }
    async fn invoke(s: &Service, call_id: &str, name: &str, args: Value) -> ApiResult<Value> {
        let call =
            serde_json::from_value(json!({"call_id":call_id,"name":name,"args":args})).unwrap();
        Ok(
            orchestration::invoke(State(s.clone()), tasks::tests::headers(), Json(call))
                .await?
                .0,
        )
    }
    #[test]
    fn preferences_are_verbatim_revisioned_atomic_and_restart_idempotent() {
        let s = tasks::tests::fixture();
        let text = "设计使用 Gemini。\n编码使用 Claude；复杂软件架构也使用 Claude。";
        let first = replace(&s, "save", text, 0).unwrap();
        assert_eq!(first.text, text);
        assert_eq!(first.revision, 1);
        let edited = replace(
            &s,
            "edit",
            "编码使用 Claude；复杂软件架构也使用 Claude。",
            1,
        )
        .unwrap();
        assert_eq!(edited.revision, 2);
        assert_eq!(
            replace(&s, "stale", "overwrite", 1).unwrap_err().0,
            StatusCode::CONFLICT
        );
        assert_eq!(
            replace(&s, "save", "different", 0).unwrap_err().0,
            StatusCode::CONFLICT
        );
        s.inner.lock().unwrap().store = Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        assert_eq!(read(&s).unwrap(), edited);
        assert_eq!(replace(&s, "save", text, 0).unwrap(), first);
        assert_eq!(read(&s).unwrap(), edited);
        let cleared = replace(&s, "clear", "", 2).unwrap();
        assert!(cleared.text.is_empty());
        assert_eq!(cleared.revision, 3);
        assert!(replace(&s, "too-long", &"a".repeat(16385), 3).is_err());
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn tools_support_saved_readback_and_reject_worker_summary_and_conflicting_calls() {
        let mut s = tasks::tests::fixture();
        turn(&s, "user-turn");
        let args = json!({"text":"Use Claude for coding.","expected_revision":0});
        let first = invoke(&s, "save", "update_execution_preferences", args.clone())
            .await
            .unwrap();
        assert_eq!(
            invoke(&s, "save", "update_execution_preferences", args)
                .await
                .unwrap(),
            first
        );
        let saved = invoke(&s, "read", "get_execution_preferences", json!({}))
            .await
            .unwrap();
        assert_eq!(saved["preferences"]["text"], "Use Claude for coding.");
        assert!(
            invoke(
                &s,
                "save",
                "update_execution_preferences",
                json!({"text":"","expected_revision":1})
            )
            .await
            .is_err()
        );
        let tool_names: Vec<_> = tool_bridge::schemas(false, false)
            .into_iter()
            .map(|v| v["name"].as_str().unwrap().to_owned())
            .collect();
        assert!(
            tool_names
                .iter()
                .any(|n| n == "update_execution_preferences")
        );
        assert!(
            !tool_bridge::schemas(true, false)
                .iter()
                .any(|v| v["name"] == "update_execution_preferences")
        );
        turn(&s, "pot-result:summary");
        assert_eq!(
            invoke(
                &s,
                "summary",
                "update_execution_preferences",
                json!({"text":"","expected_revision":1})
            )
            .await
            .unwrap_err()
            .0,
            StatusCode::FORBIDDEN
        );
        s.is_worker = true;
        assert_eq!(
            invoke(&s, "worker", "get_execution_preferences", json!({}))
                .await
                .unwrap_err()
                .0,
            StatusCode::FORBIDDEN
        );
        assert_eq!(
            replace(&s, "worker", "", 1).unwrap_err().0,
            StatusCode::FORBIDDEN
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn delegation_requires_fresh_preferences_and_pins_original_decision() {
        let mut s = tasks::tests::fixture();
        s.agent_upstream = "https://api.deepseek.com/chat/completions".into();
        initialize(&s).unwrap();
        let _ = routing::configure(
            State(s.clone()),
            tasks::tests::headers(),
            Json(routing::Policy {
                allowed_tool_profiles: ["pot-default".into()].into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap();
        replace(&s, "save", "Use DeepSeek for coding.", 0).unwrap();
        turn(&s, "delegate");
        let args = json!({"text":"Implement a parser", "profile":"pot-default","preference_revision":1,"selection_reason":"Your coding preference"});
        let task = invoke(&s, "create", "create_task", args.clone())
            .await
            .unwrap();
        assert_eq!(task["routing"]["preference_revision"], 1);
        assert_eq!(
            task["routing"]["selection_reason"],
            "Your coding preference"
        );
        assert_eq!(task["executor_icon"], "deepseek");
        replace(&s, "clear", "", 1).unwrap();
        assert_eq!(
            invoke(&s, "create", "create_task", args.clone())
                .await
                .unwrap(),
            task
        );
        assert_eq!(
            invoke(&s, "stale-create", "create_task", args)
                .await
                .unwrap_err()
                .0,
            StatusCode::CONFLICT
        );
        let refreshed = context(&s).unwrap();
        assert!(refreshed.contains("\"revision\":2"));
        assert!(!refreshed.contains("Use DeepSeek for coding."));
        // Missing/unauthorized choices cannot quietly create a default task.
        assert!(
            invoke(
                &s,
                "missing",
                "create_task",
                json!({"text":"test","profile":"missing","preference_revision":2})
            )
            .await
            .is_err()
        );
        let page = tasks::page(&s, &TaskQuery::default()).unwrap();
        assert_eq!(page.items.len(), 1);
        assert_eq!(
            page.items[0].routing.as_ref().unwrap().preference_revision,
            Some(1)
        );
        // Restart keeps the preference clear and the old immutable task decision.
        tasks::restore(&s).unwrap();
        assert_eq!(
            tasks::page(&s, &TaskQuery::default()).unwrap().items[0],
            page.items[0]
        );
        let l = s.usage.lock().unwrap();
        let marked: u64 =
            l.0.query_row("SELECT COUNT(*) FROM routing_turns", [], |r| r.get(0))
                .unwrap();
        assert_eq!(marked, 1);
        drop(l);
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn temporary_choices_do_not_save_preferences_and_stale_nonempty_context_is_rejected() {
        let s = tasks::tests::fixture();
        turn(&s, "override");
        let task = invoke(&s, "create", "create_task", json!({"text":"test"}))
            .await
            .unwrap();
        assert_eq!(read(&s).unwrap(), Preferences::default());
        replace(&s, "save", "Use DeepSeek for coding.", 0).unwrap();
        assert!(
            invoke(&s, "no-revision", "create_task", json!({"text":"test"}))
                .await
                .is_err()
        );
        assert_eq!(
            tasks::page(&s, &TaskQuery::default()).unwrap().items[0].id,
            task["id"]
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    #[tokio::test]
    async fn catalog_is_credential_free_and_excludes_unauthorized_profiles() {
        let s = tasks::tests::fixture();
        initialize(&s).unwrap();
        let mut profiles = profiles(&s.inner.lock().unwrap().store).unwrap();
        let mut unauthorized = profiles[0].clone();
        unauthorized.id = "unauthorized".into();
        unauthorized.name = "Unauthorized".into();
        unauthorized.provider = "missing".into();
        unauthorized.auth_ref = "providers.json:missing".into();
        profiles.push(unauthorized);
        s.inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&profiles).unwrap(),
            )
            .unwrap();
        let _ = routing::configure(
            State(s.clone()),
            tasks::tests::headers(),
            Json(routing::Policy {
                allowed_tool_profiles: ["pot-default".into()].into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap();
        let snapshot = inspect(&s).unwrap();
        assert_eq!(snapshot["profiles"][0]["eligible"], true);
        assert_eq!(snapshot["profiles"][1]["eligible"], false);
        assert!(!snapshot.to_string().contains("api_key"));
        assert!(!snapshot.to_string().contains("fixture"));
        assert!(
            routing::resolve(
                &s,
                Some("unauthorized"),
                routing::Source::Tool,
                &TaskRequirements::default()
            )
            .is_err()
        );
        std::fs::remove_dir_all(&s.data).unwrap();
    }
}

#[cfg(test)]
mod integration_tests {
    use super::*;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[tokio::test(flavor = "multi_thread", worker_threads = 4)]
    async fn native_agent_execution_preferences_tools_and_routing_usage() {
        use axum::response::sse::Event as WireEvent;
        let mut s = tasks::tests::fixture();
        s.execution.as_mut().unwrap().max_requests_per_turn = 8;
        let _ = routing::configure(
            State(s.clone()),
            tasks::tests::headers(),
            Json(routing::Policy {
                allowed_tool_profiles: ["pot-default".into()].into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        s.agent_upstream = format!("http://127.0.0.1:{port}/mock");
        execution::private_json(
            &s.data.join(RUNTIME_ENDPOINT_FILE),
            &json!(RuntimeEndpoint {
                api_version: API_VERSION,
                port,
                token: s.token.clone(),
                boot_id: s.boot.clone()
            }),
        )
        .unwrap();
        let captures = Arc::new(Mutex::new(Vec::<Value>::new()));
        let captured = captures.clone();
        let counter = Arc::new(AtomicUsize::new(0));
        let count = counter.clone();
        let router=Router::new()
            .route("/gw/{provider}/{*path}",post(gateway::routed_protocol))
            .route("/api/v1/orchestration",post(orchestration::invoke))
            .route("/api/v1/mcp",post(tool_bridge::mcp))
            .route("/api/agent/compactions",post(execution::compaction_event))
            .with_state(s.clone())
            .route("/mock",post(move |Json(body):Json<Value>| {
                let captures=captured.clone(); let counter=count.clone();
                async move {
                    let step=counter.fetch_add(1,Ordering::SeqCst);
                    let names:Vec<_>=body["tools"].as_array().unwrap().iter().filter_map(|v|v["function"]["name"].as_str()).collect();
                    assert!(names.contains(&"get_execution_preferences"));
                    assert!(names.contains(&"update_execution_preferences"));
                    captures.lock().unwrap().push(body);
                    let (name,args)=match step {
                        0 => ("get_execution_preferences",json!({})),
                        1 => ("update_execution_preferences",json!({"text":"Use DeepSeek for coding.","expected_revision":0})),
                        2 => ("get_execution_preferences",json!({})),
                        3 => ("create_task",json!({"text":"Build parser","profile":"pot-default","preference_revision":1,"selection_reason":"Your coding preference"})),
                        _ => ("",json!({})),
                    };
                    let delta=if name.is_empty() {json!({"role":"assistant","content":"Preference saved and task accepted."})} else {
                        json!({"role":"assistant","tool_calls":[{"index":0,"id":format!("prefs-{step}"),"type":"function","function":{"name":name,"arguments":args.to_string()}}]})
                    };
                    let chunk=|d:Value,f:Value|json!({"id":format!("prefs-{step}"),"object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":d,"finish_reason":f}]}).to_string();
                    let frames=vec![WireEvent::default().data(chunk(delta,Value::Null)),
                        WireEvent::default().data(chunk(json!({}),json!(if name.is_empty(){"stop"}else{"tool_calls"}))),
                        WireEvent::default().data(json!({"id":format!("prefs-{step}"),"object":"chat.completion.chunk","choices":[],"usage":{"prompt_tokens":20,"completion_tokens":5}}).to_string()),
                        WireEvent::default().data("[DONE]")];
                    Sse::new(futures::stream::iter(frames.into_iter().map(Ok::<_,Infallible>)))
                }
            }));
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        let _ = crate::send(
            State(s.clone()),
            tasks::tests::headers(),
            Json(SendMessage {
                request_id: "prefs-integration".into(),
                conversation_id: "main".into(),
                text: "Remember coding with DeepSeek, then build a parser.".into(),
            }),
        )
        .await
        .unwrap();
        let mut request = rx.recv().await.unwrap();
        request.text.push_str(&context(&s).unwrap());
        tokio::time::timeout(
            std::time::Duration::from_secs(45),
            execution::run(&s, request),
        )
        .await
        .unwrap()
        .unwrap();
        assert_eq!(counter.load(Ordering::SeqCst), 5);
        assert_eq!(read(&s).unwrap().text, "Use DeepSeek for coding.");
        let task = tasks::page(&s, &TaskQuery::default())
            .unwrap()
            .items
            .remove(0);
        assert_eq!(task.routing.unwrap().preference_revision, Some(1));
        assert_eq!(task.executor_icon, "custom");
        assert!(
            !s.snapshot()
                .unwrap()
                .messages
                .iter()
                .any(|m| m.status == MessageStatus::Failed)
        );
        let all = captures.lock().unwrap();
        assert!(
            all[3]["messages"]
                .to_string()
                .contains("Use DeepSeek for coding.")
        );
        drop(all);
        let usage = s
            .usage
            .lock()
            .unwrap()
            .summary(
                &usage::QueryArgs {
                    days: 7,
                    timezone: "Asia/Shanghai".into(),
                    scope: "main".into(),
                    offset: 0,
                },
                now(),
            )
            .unwrap();
        assert_eq!(usage["routing_total"], usage["lifetime"]);
        assert_eq!(usage["routing_total"]["requests"], 5);
        assert_eq!(usage["routing_total"]["unknown"], 0);
        server.abort();
        std::fs::remove_dir_all(&s.data).unwrap();
    }
}

#[cfg(test)]
mod quality_tests;
