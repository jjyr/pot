use super::*;
use axum::response::sse::Event as WireEvent;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "Runs installed Pi with native protocol mock providers; set POT_TEST_PI_BIN"]
async fn real_pi_native_gateway_codecs_preserve_wire_and_account_usage() {
    for protocol in [
        Protocol::OpenaiResponses,
        Protocol::AnthropicMessages,
        Protocol::Gemini,
    ] {
        let dir = std::env::temp_dir().join(format!("pot-codec-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.pi_program = std::env::var("POT_TEST_PI_BIN").unwrap();
        s.pi = Some(pi::Config {
            api_key: "legacy".into(),
            model: "mock-model".into(),
            request_limit: 10,
            max_tokens: 128,
            max_requests_per_turn: 3,
            max_parallel_tasks: 5,
        });
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: dir.clone(),
            limit: 10,
            max_output_tokens: 128,
        });
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        pi::private_json(
            &dir.join(RUNTIME_ENDPOINT_FILE),
            &json!({"api_version":API_VERSION,"port":port,"token":s.token,"boot_id":s.boot}),
        )
        .unwrap();
        let path = if protocol == Protocol::Gemini {
            "/models/mock-model:streamGenerateContent"
        } else {
            "/model"
        };
        pi::private_json(&dir.join("providers.json"),&json!([{"id":"mock","endpoint":format!("http://127.0.0.1:{port}{path}"),"allowed_hosts":["127.0.0.1"],"api_key":"upstream-secret","protocol":protocol}])).unwrap();
        let seen = Arc::new(Mutex::new(Vec::new()));
        let capture = seen.clone();
        let p = protocol.clone();
        let mock = move |h: HeaderMap, Json(body): Json<Value>| {
            let p = p.clone();
            let capture = capture.clone();
            async move {
                capture.lock().unwrap().push((h, body));
                let events = match p {
                    Protocol::OpenaiResponses => vec![
                        json!({"type":"response.created","response":{"id":"resp_test","model":"mock-model","status":"in_progress","output":[]}}),
                        json!({"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_test","role":"assistant","content":[],"status":"in_progress"}}),
                        json!({"type":"response.content_part.added","item_id":"msg_test","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}),
                        json!({"type":"response.output_text.delta","item_id":"msg_test","output_index":0,"content_index":0,"delta":"codec done"}),
                        json!({"type":"response.output_text.done","item_id":"msg_test","output_index":0,"content_index":0,"text":"codec done"}),
                        json!({"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_test","role":"assistant","content":[{"type":"output_text","text":"codec done","annotations":[]}],"status":"completed"}}),
                        json!({"type":"response.completed","response":{"id":"resp_test","model":"mock-model","status":"completed","output":[],"usage":{"input_tokens":33,"output_tokens":5,"input_tokens_details":{"cached_tokens":10}}}}),
                    ],
                    Protocol::AnthropicMessages => vec![
                        json!({"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"mock-model","content":[],"stop_reason":null,"usage":{"input_tokens":20,"cache_read_input_tokens":10,"cache_creation_input_tokens":3,"output_tokens":0}}}),
                        json!({"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}),
                        json!({"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"codec done"}}),
                        json!({"type":"content_block_stop","index":0}),
                        json!({"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}),
                        json!({"type":"message_stop"}),
                    ],
                    Protocol::Gemini => vec![
                        json!({"candidates":[{"content":{"role":"model","parts":[{"text":"codec done"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":33,"candidatesTokenCount":3,"thoughtsTokenCount":2,"cachedContentTokenCount":10},"modelVersion":"mock-model"}),
                    ],
                    _ => unreachable!(),
                };
                Sse::new(futures::stream::iter(events.into_iter().map(|value| {
                    let event = WireEvent::default().data(value.to_string());
                    Ok::<_, Infallible>(if let Some(kind) = value["type"].as_str() {
                        event.event(kind)
                    } else {
                        event
                    })
                })))
            }
        };
        let router = Router::new()
            .route("/gw/{provider}/{*path}", post(routed_protocol))
            .with_state(s.clone())
            .route(path, post(mock));
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let mut profile = agent::Profile::legacy(s.pi.as_ref().unwrap());
        profile.id = "native-mock".into();
        profile.provider = "mock".into();
        profile.auth_ref = "providers.json:mock".into();
        s.pi = None;
        s.api_budget = None;
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let settings = serde_json::from_value(
            json!({"profile":profile,"expected_revision":0,"request_id":"select"}),
        )
        .unwrap();
        let _ = agent::migration::settings(State(s.clone()), h, Json(settings))
            .await
            .unwrap();
        s.save(Message {
            id: "turn".into(),
            conversation_id: "main".into(),
            role: Role::User,
            text: "hello".into(),
            status: MessageStatus::Accepted,
            created_at_ms: now(),
            request_id: Some("turn".into()),
        })
        .unwrap();
        s.turn(Some(TurnState {
            user_message_id: "turn".into(),
            cancelling: false,
        }));
        agent::run(
            &s,
            SendMessage {
                request_id: "turn".into(),
                conversation_id: "main".into(),
                text: "hello".into(),
            },
        )
        .await
        .unwrap();
        assert_eq!(
            s.inner
                .lock()
                .unwrap()
                .store
                .message("turn")
                .unwrap()
                .unwrap()
                .status,
            MessageStatus::Complete,
            "{protocol:?}"
        );
        let captures = seen.lock().unwrap();
        assert_eq!(captures.len(), 1, "{protocol:?}");
        let (headers, body) = &captures[0];
        let (auth, cap) = match protocol {
            Protocol::OpenaiResponses => ("authorization", "/max_output_tokens"),
            Protocol::AnthropicMessages => ("x-api-key", "/max_tokens"),
            Protocol::Gemini => ("x-goog-api-key", "/generationConfig/maxOutputTokens"),
            _ => unreachable!(),
        };
        assert_eq!(
            headers[auth],
            if auth == "authorization" {
                "Bearer upstream-secret"
            } else {
                "upstream-secret"
            }
        );
        assert!(body.pointer(cap).unwrap().as_u64().unwrap() <= 128);
        assert!(!body.to_string().contains("upstream-secret"));
        drop(captures);
        let usage: (u64, u64, u64, u64) = s
            .usage
            .lock()
            .unwrap()
            .0
            .query_row(
                "SELECT input,output,cache,reserved FROM requests",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?, r.get(3)?)),
            )
            .unwrap();
        assert_eq!(usage, (33, 5, 10, 0), "{protocol:?}");
        server.abort();
        std::fs::remove_dir_all(dir).unwrap();
    }
}
