//! Opt-in real-main-model evaluation with synthetic, unexecuted worker Profiles.
use super::*;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "Paid main-model evaluation; set POT_TEST_REAL_MODEL_CONFIG explicitly"]
async fn real_model_execution_preferences_quality() {
    let path = std::env::var("POT_TEST_REAL_MODEL_CONFIG").expect("POT_TEST_REAL_MODEL_CONFIG");
    let config: Value = serde_json::from_slice(&std::fs::read(path).unwrap()).unwrap();
    let provider = &config["providers"]["deepseek"];
    assert_eq!(
        provider["baseUrl"].as_str().unwrap().trim_end_matches('/'),
        "https://api.deepseek.com"
    );
    let configured_key = provider["apiKey"].as_str().unwrap();
    assert!(
        !configured_key.starts_with('!'),
        "Command-based credentials are not used by tests"
    );
    let key = if configured_key
        .bytes()
        .all(|b| b.is_ascii_uppercase() || b == b'_' || b.is_ascii_digit())
    {
        std::env::var(configured_key)
            .expect("Configured credential environment variable is unavailable")
    } else {
        configured_key.into()
    };
    let preferences = "Use Claude Coding for coding, debugging, and refactoring. Use Gemini Design for fiction, document organization and general design. Use Claude Coding for complex software architecture. An explicit choice for this task takes precedence.";
    // Expected IDs are annotated before evaluation, not inferred from the model's explanation.
    let cases = [
        (
            "coding",
            "实现一个 CSV 解析器并写入项目文件。",
            "claude-coding",
        ),
        (
            "debugging",
            "排查服务偶发死锁，给出代码修复。",
            "claude-coding",
        ),
        (
            "fiction",
            "写一篇海边小镇的短篇小说，保存为文档。",
            "gemini-design",
        ),
        (
            "documents",
            "整理这些会议纪要并生成分类索引文件：10月1日产品会决定首页采用卡片布局，李负责设计；10月2日技术会决定增加缓存，王负责实现。按产品和技术分类。",
            "gemini-design",
        ),
        (
            "documents-missing-input",
            "整理散乱会议纪要并生成分类索引文件。",
            "clarify-documents",
        ),
        (
            "visual-design",
            "设计一个应用首页的视觉布局并保存设计稿。",
            "gemini-design",
        ),
        (
            "architecture-exception",
            "设计复杂支付系统的软件架构并保存方案。",
            "claude-coding",
        ),
        (
            "override",
            "这次明确用 Gemini Design 来实现 CSV 解析器并保存代码，不修改长期偏好。",
            "gemini-design",
        ),
        (
            "mixed-deliverable",
            "重构登录模块，并为这个编码任务补充 README。",
            "claude-coding",
        ),
        (
            "discussion",
            "Claude 好像更适合编码，你觉得呢？只讨论，不执行任务。",
            "discussion",
        ),
        (
            "save",
            "以后编码用 Claude Coding，设计用 Gemini Design，记住这个执行偏好。",
            "save",
        ),
        (
            "read",
            "我设置了哪些模型选择偏好？请完整展示当前保存的内容。",
            "read",
        ),
        ("clear", "清空我的所有执行偏好。", "clear"),
    ];
    let selected_case = std::env::var("POT_TEST_ROUTING_CASE").ok();
    let mut failures = Vec::new();
    let mut evaluated = 0;
    let mut calls = 0;
    let mut input = 0;
    let mut output = 0;
    for (label, text, expected) in cases {
        if selected_case
            .as_ref()
            .is_some_and(|selected| !selected.split(',').any(|v| v == label))
        {
            continue;
        }
        evaluated += 1;
        let mut s = tasks::tests::fixture();
        let main = s.execution.as_mut().unwrap();
        main.api_key = key.clone();
        main.max_tokens = 2048;
        main.request_limit = 20;
        main.max_requests_per_turn = 8;
        s.agent_upstream = "https://api.deepseek.com/chat/completions".into();
        initialize(&s).unwrap();
        let base = profiles(&s.inner.lock().unwrap().store).unwrap().remove(0);
        let mut claude = base.clone();
        claude.id = "claude-coding".into();
        claude.name = "Claude Coding".into();
        claude.provider = "eval-claude".into();
        claude.auth_ref = "providers.json:eval-claude".into();
        claude.model = "claude-evaluation-fixture".into();
        let mut gemini = base.clone();
        gemini.id = "gemini-design".into();
        gemini.name = "Gemini Design".into();
        gemini.provider = "eval-gemini".into();
        gemini.auth_ref = "providers.json:eval-gemini".into();
        gemini.model = "gemini-evaluation-fixture".into();
        s.inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.profiles.v1",
                &serde_json::to_string(&vec![base, claude, gemini]).unwrap(),
            )
            .unwrap();
        execution::private_json(&s.data.join("providers.json"),&json!([
            {"id":"eval-claude","endpoint":"https://api.anthropic.com/v1/messages","allowed_hosts":["api.anthropic.com"],"api_key":"synthetic-never-called","protocol":"anthropic_messages"},
            {"id":"eval-gemini","endpoint":"https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent","allowed_hosts":["generativelanguage.googleapis.com"],"api_key":"synthetic-never-called","protocol":"gemini"}
        ])).unwrap();
        let _ = routing::configure(
            State(s.clone()),
            tasks::tests::headers(),
            Json(routing::Policy {
                allowed_tool_profiles: [
                    "pot-default".into(),
                    "claude-coding".into(),
                    "gemini-design".into(),
                ]
                .into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap();
        if expected != "save" {
            replace(&s, "seed", preferences, 0).unwrap();
        }
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
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
        let router = Router::new()
            .route("/gw/{provider}/{*path}", post(gateway::routed_protocol))
            .route("/api/v1/orchestration", post(orchestration::invoke))
            .route("/api/v1/mcp", post(tool_bridge::mcp))
            .route("/api/agent/compactions", post(execution::compaction_event))
            .with_state(s.clone());
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        let _ = crate::send(
            State(s.clone()),
            tasks::tests::headers(),
            Json(SendMessage {
                request_id: label.into(),
                conversation_id: "main".into(),
                text: text.into(),
            }),
        )
        .await
        .unwrap();
        let mut request = rx.recv().await.unwrap();
        request.text.push_str(&context(&s).unwrap());
        let result = tokio::time::timeout(
            std::time::Duration::from_secs(90),
            execution::run(&s, request),
        )
        .await;
        if !matches!(result, Ok(Ok(()))) {
            failures.push(format!("{label}: turn did not complete"));
        }
        let saved = read(&s).unwrap();
        let tasks = tasks::page(&s, &TaskQuery::default()).unwrap().items;
        let snapshot = s.snapshot().unwrap();
        let answer = snapshot
            .messages
            .iter()
            .filter(|m| m.role == Role::Assistant)
            .map(|m| m.text.as_str())
            .collect::<Vec<_>>()
            .join("\n");
        let passed = match expected {
            "clarify-documents" => {
                saved.text == preferences
                    && saved.revision == 1
                    && if tasks.is_empty() {
                        answer.contains("Gemini Design")
                    } else {
                        tasks.len() == 1
                            && tasks[0].routing.as_ref().is_some_and(|r| {
                                r.profile == "gemini-design" && r.preference_revision == Some(1)
                            })
                    }
            }
            "discussion" => saved.text == preferences && saved.revision == 1 && tasks.is_empty(),
            "save" => {
                saved.revision == 1
                    && saved.text.contains("Claude Coding")
                    && saved.text.contains("Gemini Design")
                    && tasks.is_empty()
            }
            "read" => saved.text == preferences && tasks.is_empty() && answer.contains(preferences),
            "clear" => saved.text.is_empty() && saved.revision == 2 && tasks.is_empty(),
            profile => {
                tasks.len() == 1
                    && tasks[0]
                        .routing
                        .as_ref()
                        .is_some_and(|r| r.profile == profile && r.preference_revision == Some(1))
                    && saved.text == preferences
                    && saved.revision == 1
            }
        };
        if !passed {
            eprintln!(
                "Failed case {label}: routes {:?}, saved revision {}, answer {}",
                tasks
                    .iter()
                    .filter_map(|t| t.routing.as_ref().map(|r| &r.profile))
                    .collect::<Vec<_>>(),
                saved.revision,
                answer.chars().take(1200).collect::<String>()
            );
            failures.push(format!("{label}: wrong preference/route/readback"));
        }
        let totals = s
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
        calls += totals["lifetime"]["requests"].as_u64().unwrap();
        input += totals["lifetime"]["input"].as_u64().unwrap();
        output += totals["lifetime"]["output"].as_u64().unwrap();
        eprintln!(
            "Real routing evaluation {label}: {}",
            if passed { "PASS" } else { "FAIL" }
        );
        server.abort();
        std::fs::remove_dir_all(&s.data).unwrap();
    }
    eprintln!(
        "Real routing evaluation: {evaluated} cases, {calls} model requests, {input} input, {output} output tokens; no worker model calls"
    );
    assert!(failures.is_empty(), "{}", failures.join("; "));
}
