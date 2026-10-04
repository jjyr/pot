//! Pi is a guest subprocess. Rust owns the API, request ledger and UI transcript;
//! Pi owns its native JSONL session and built-in tools.
use super::*;
use axum::{body::Body, response::Response};
use futures::StreamExt;
use std::{collections::HashMap, io::Write, os::unix::fs::OpenOptionsExt, time::Duration};
use tokio::io::AsyncWriteExt;

#[derive(Clone, Deserialize)]
pub struct Config {
    pub api_key: String,
    pub model: String,
    pub request_limit: u64,
    pub max_tokens: u32,
    pub max_requests_per_turn: u64,
}
impl Config {
    pub fn load(path: &std::path::Path) -> Result<Self> {
        let c: Self = serde_json::from_slice(&std::fs::read(path)?)?;
        anyhow::ensure!(
            c.model == "deepseek-v4-flash" && !c.api_key.is_empty(),
            "Pi requires DeepSeek Flash configuration"
        );
        anyhow::ensure!(
            c.request_limit <= 10000
                && (1..=4096).contains(&c.max_tokens)
                && (1..=12).contains(&c.max_requests_per_turn),
            "Invalid Pi request limits"
        );
        Ok(c)
    }
}
pub fn private_json(path: &std::path::Path, value: &Value) -> Result<()> {
    let temp = path.with_extension("tmp");
    let mut file = std::fs::OpenOptions::new()
        .create(true)
        .truncate(true)
        .write(true)
        .mode(0o600)
        .open(&temp)?;
    file.write_all(&serde_json::to_vec(value)?)?;
    file.sync_all()?;
    std::fs::rename(temp, path)?;
    Ok(())
}

// The gateway has a fixed upstream and never follows redirects. The real API
// key is not passed to Pi or included in its session files.
pub async fn gateway(
    State(s): State<Service>,
    h: HeaderMap,
    Json(mut body): Json<Value>,
) -> ApiResult<Response> {
    s.authorized(&h)?;
    let c =
        s.pi.as_ref()
            .ok_or_else(|| error(StatusCode::NOT_FOUND, "pi_disabled", "Pi is not configured"))?;
    if body["model"] != c.model || body["stream"] != true || body.to_string().len() > 96000 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_model_request",
            "Model, stream or context limit exceeded",
        ));
    }
    body["max_tokens"] = json!(c.max_tokens);
    body.as_object_mut()
        .ok_or_else(|| error(StatusCode::BAD_REQUEST, "invalid_body", "Expected object"))?
        .remove("max_completion_tokens");
    body["thinking"] = json!({"type":"disabled"});
    body["stream_options"] = json!({"include_usage":true});
    {
        let mut inner = s.inner.lock().unwrap();
        let turn = inner
            .turn
            .as_ref()
            .ok_or_else(|| error(StatusCode::CONFLICT, "no_turn", "No active turn"))?;
        if turn.cancelling {
            return Err(error(
                StatusCode::CONFLICT,
                "cancelling",
                "Turn is stopping",
            ));
        }
        if inner.model_requests >= c.max_requests_per_turn {
            return Err(error(
                StatusCode::TOO_MANY_REQUESTS,
                "turn_budget",
                "Turn request limit reached",
            ));
        }
        let budget = s.api_budget.as_ref().unwrap();
        deepseek::reserve_in(&budget.data.join(budget.namespace), budget.limit).map_err(|_| {
            error(
                StatusCode::TOO_MANY_REQUESTS,
                "budget_exhausted",
                "Request budget exhausted",
            )
        })?;
        inner.model_requests += 1;
    }
    s.publish_budget().map_err(internal)?;
    let response = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(120))
        .build()
        .map_err(|e| internal(e.into()))?
        .post(&s.pi_upstream)
        .bearer_auth(&c.api_key)
        .json(&body)
        .send()
        .await
        .map_err(|_| {
            error(
                StatusCode::BAD_GATEWAY,
                "provider_connection",
                "DeepSeek connection failed",
            )
        })?;
    if !response.status().is_success() {
        return Err(error(
            StatusCode::BAD_GATEWAY,
            "provider_rejected",
            &format!("DeepSeek returned HTTP {}", response.status().as_u16()),
        ));
    }
    let mut incoming = response.bytes_stream();
    let service = s.clone();
    let stream = async_stream::stream! {
        let mut pending = String::new();
        while let Some(chunk) = incoming.next().await {
            match chunk {
                Ok(bytes) => {
                    // Usage fields are ASCII; partial UTF-8 text is only forwarded,
                    // never reconstructed here. Bound diagnostics independently.
                    pending.push_str(&String::from_utf8_lossy(&bytes));
                    while let Some(end) = pending.find('\n') {
                        let line = pending[..end].trim().to_owned(); pending.drain(..=end);
                        if let Some(data) = line.strip_prefix("data:") {
                            if let Ok(value) = serde_json::from_str::<Value>(data.trim()) {
                                if value["usage"].is_object() {
                                    let inner = service.inner.lock().unwrap();
                                    let mut list: Vec<Value> = inner.store.get("pi.usage").ok().flatten().and_then(|v|serde_json::from_str(&v).ok()).unwrap_or_default();
                                    list.push(value["usage"].clone());
                                    let _ = inner.store.set("pi.usage",&json!(list).to_string());
                                }
                            }
                        }
                    }
                    if pending.len()>1024*1024 { pending.clear(); }
                    yield Ok::<_,std::io::Error>(bytes);
                }
                Err(_) => {yield Err(std::io::Error::other("Provider stream interrupted")); break;}
            }
        }
    };
    Ok(Response::builder()
        .header("content-type", "text/event-stream")
        .header("cache-control", "no-cache")
        .body(Body::from_stream(stream))
        .unwrap())
}
fn text(value: &Value) -> String {
    value
        .as_array()
        .map(|blocks| {
            blocks
                .iter()
                .filter_map(|b| b["text"].as_str())
                .collect::<Vec<_>>()
                .join("\n")
        })
        .unwrap_or_default()
}
fn limited(s: String) -> String {
    if s.len() <= 24000 {
        s
    } else {
        format!(
            "{}\n…（输出已截断，完整结果在 Pi 会话中）",
            s.chars().take(6000).collect::<String>()
        )
    }
}
#[derive(Default)]
struct Transcript {
    assistant: Option<Message>,
    tools: HashMap<String, Message>,
    failure: Option<String>,
}
impl Transcript {
    fn record(&mut self, s: &Service, e: &Value) -> Result<()> {
        match e["type"].as_str().unwrap_or("") {
            "message_start" if e["message"]["role"] == "assistant" => {
                let m = Message {
                    id: id(),
                    conversation_id: "main".into(),
                    role: Role::Assistant,
                    text: String::new(),
                    status: MessageStatus::Streaming,
                    created_at_ms: now(),
                    request_id: None,
                };
                s.save(m.clone())?;
                self.assistant = Some(m);
            }
            "message_update" if e["assistantMessageEvent"]["type"] == "text_delta" => {
                if let Some(m) = self.assistant.as_mut() {
                    m.text
                        .push_str(e["assistantMessageEvent"]["delta"].as_str().unwrap_or(""));
                    s.save(m.clone())?;
                }
            }
            "message_end" if e["message"]["role"] == "assistant" => {
                if let Some(m) = self.assistant.as_mut() {
                    let final_message = &e["message"];
                    m.text = text(&final_message["content"]);
                    m.status = if cancelled(s) {
                        MessageStatus::Cancelled
                    } else {
                        match final_message["stopReason"].as_str() {
                            Some("aborted") => MessageStatus::Cancelled,
                            Some("error") => MessageStatus::Failed,
                            Some("length") => MessageStatus::Failed,
                            _ => MessageStatus::Complete,
                        }
                    };
                    if m.status == MessageStatus::Failed {
                        let detail = final_message["errorMessage"]
                            .as_str()
                            .unwrap_or("模型输出达到上限，请缩小任务后继续")
                            .to_string();
                        m.text.push_str(&format!("\n{detail}"));
                        self.failure = Some(detail);
                    }
                    if m.text.is_empty() && m.status == MessageStatus::Cancelled {
                        m.text = "已停止".into();
                    }
                    if m.text.is_empty() && m.status == MessageStatus::Complete {
                        m.text = "调用工具".into();
                    }
                    s.save(m.clone())?;
                }
            }
            "tool_execution_start" => {
                let key = e["toolCallId"]
                    .as_str()
                    .ok_or_else(|| anyhow!("Missing tool ID"))?
                    .to_string();
                let m = Message {
                    id: id(),
                    conversation_id: "main".into(),
                    role: Role::Tool,
                    text: limited(format!(
                        "{}\n{}",
                        e["toolName"].as_str().unwrap_or("tool"),
                        e["args"]
                    )),
                    status: MessageStatus::Streaming,
                    created_at_ms: now(),
                    request_id: None,
                };
                s.save(m.clone())?;
                self.tools.insert(key, m);
            }
            "tool_execution_end" => {
                if let Some(m) = self.tools.get_mut(e["toolCallId"].as_str().unwrap_or("")) {
                    m.text = limited(format!("{}\n{}", m.text, text(&e["result"]["content"])));
                    m.status = if cancelled(s) {
                        MessageStatus::Cancelled
                    } else if e["isError"] == true {
                        MessageStatus::Failed
                    } else {
                        MessageStatus::Complete
                    };
                    s.save(m.clone())?;
                }
            }
            _ => {}
        }
        Ok(())
    }
    fn finish(&mut self, s: &Service, status: MessageStatus, detail: &str) -> Result<()> {
        for m in self.assistant.iter_mut().chain(self.tools.values_mut()) {
            if m.status == MessageStatus::Streaming {
                m.status = status;
                m.text.push_str(detail);
                s.save(m.clone())?;
            }
        }
        Ok(())
    }
}
async fn command(stdin: &mut tokio::process::ChildStdin, value: Value) -> Result<()> {
    let mut bytes = serde_json::to_vec(&value)?;
    bytes.push(b'\n');
    stdin.write_all(&bytes).await?;
    stdin.flush().await?;
    Ok(())
}
fn cancelled(s: &Service) -> bool {
    s.inner
        .lock()
        .unwrap()
        .turn
        .as_ref()
        .is_some_and(|t| t.cancelling)
}

pub async fn run(s: &Service, req: SendMessage) -> Result<()> {
    let mut user = s
        .inner
        .lock()
        .unwrap()
        .store
        .duplicate(&req.request_id)?
        .ok_or_else(|| anyhow!("Missing accepted message"))?;
    let mut transcript = Transcript::default();
    let result = execute(s, &req, &mut transcript).await;
    let status = if cancelled(s) && result.is_ok() {
        MessageStatus::Cancelled
    } else if result.is_err() {
        MessageStatus::Failed
    } else {
        MessageStatus::Complete
    };
    let detail = result
        .as_ref()
        .err()
        .map(|e| format!("\n{e}"))
        .unwrap_or_default();
    transcript.finish(s, status, &detail)?;
    user.status = status;
    s.save(user)?;
    let uncertain = result
        .as_ref()
        .err()
        .is_some_and(|e| e.to_string().contains("未确认停止"));
    if let Err(e) = result {
        s.save(Message {
            id: id(),
            conversation_id: "main".into(),
            role: Role::System,
            text: format!("任务未完成：{e}"),
            status,
            created_at_ms: now(),
            request_id: None,
        })?;
    }
    // A failed request is recoverable; keep the composer usable for a correction.
    s.phase(
        if uncertain {
            EnginePhase::Failed
        } else {
            EnginePhase::Ready
        },
        if uncertain {
            Some("Pi 未确认停止，请 Quit 后重开环境".into())
        } else {
            None
        },
    );
    Ok(())
}
async fn execute(s: &Service, req: &SendMessage, transcript: &mut Transcript) -> Result<()> {
    if cancelled(s) {
        return Ok(());
    }
    let config = s.pi.as_ref().unwrap();
    let home = s.data.join("pi-home");
    std::fs::create_dir_all(&home)?;
    let session = s.data.join("pi-session.jsonl");
    let previous = s.inner.lock().unwrap().store.get("pi.session")?;
    if previous.is_none() && !session.exists() {
        std::fs::OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(&session)?
            .sync_all()?;
    }
    if previous.is_some() {
        anyhow::ensure!(session.exists(), "Pi 会话文件缺失，拒绝静默新建会话");
    }
    let endpoint: RuntimeEndpoint =
        serde_json::from_slice(&std::fs::read(s.data.join(RUNTIME_ENDPOINT_FILE))?)?;
    private_json(
        &home.join("models.json"),
        &json!({"providers":{"deepseek":{"baseUrl":format!("http://127.0.0.1:{}/api/pi/v1",endpoint.port),"api":"openai-completions","apiKey":s.token,"models":[{"id":config.model,"name":"DeepSeek Flash","reasoning":false,"input":["text"],"contextWindow":32768,"maxTokens":config.max_tokens,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0}}]}}}),
    )?;
    private_json(
        &home.join("settings.json"),
        &json!({"retry":{"enabled":false,"provider":{"maxRetries":0}},"compaction":{"enabled":false},"enableInstallTelemetry":false,"enableAnalytics":false,"shellPath":"/bin/bash"}),
    )?;
    let mut child = Command::new(&s.pi_program)
        .args([
            "--mode",
            "rpc",
            "--provider",
            "deepseek",
            "--model",
            &config.model,
            "--thinking",
            "off",
            "--tools",
            "read,write,edit,bash,grep,find,ls",
            "--no-extensions",
            "--no-skills",
            "--no-prompt-templates",
            "--no-context-files",
            "--no-approve",
            "--session",
        ])
        .arg(&session)
        .env("PI_CODING_AGENT_DIR", &home)
        .env("PI_SKIP_VERSION_CHECK", "1")
        .current_dir(&s.workspace)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .kill_on_drop(true)
        .process_group(0)
        .spawn()?;
    let pid = child.id().unwrap();
    let mut stdin = child.stdin.take().unwrap();
    let mut lines = BufReader::new(child.stdout.take().unwrap()).lines();
    let result=async {
        command(&mut stdin,json!({"id":"init","type":"get_state"})).await?;
        let mut started=false;let mut abort_sent=false;
        let deadline=tokio::time::sleep(Duration::from_secs(180));tokio::pin!(deadline);
        loop {
            tokio::select! {
                _=&mut deadline => bail!("任务超时，已停止 Pi"),
                _=s.cancel_notify.notified(), if !abort_sent => {
                    if cancelled(s) {command(&mut stdin,json!({"id":"stop","type":"abort"})).await?;abort_sent=true;deadline.as_mut().reset(tokio::time::Instant::now()+Duration::from_secs(8));}
                }
                line=lines.next_line()=> {
                    let line=line?.ok_or_else(||anyhow!("Pi 进程意外退出"))?;
                    anyhow::ensure!(line.len()<=4*1024*1024,"Pi event too large");
                    let e:Value=serde_json::from_str(&line)?;
                    if e["type"]=="response" {
                        if e["success"]==false {bail!("Pi command failed: {}",e["error"].as_str().unwrap_or("unknown"));}
                        if e["id"]=="init" {
                            let session_id=e["data"]["sessionId"].as_str().ok_or_else(||anyhow!("Pi session ID missing"))?;
                            if let Some(old)=&previous {anyhow::ensure!(old==session_id,"Pi session changed unexpectedly");}
                            std::fs::File::open(&session)?.sync_all()?;
                            s.inner.lock().unwrap().store.set("pi.session",session_id)?;
                            if cancelled(s) {return Ok(());}
                            command(&mut stdin,json!({"id":req.request_id,"type":"prompt","message":req.text})).await?;started=true;
                        }
                        if e["id"]=="stop" {return Ok(());}
                    }
                    if started && e["type"]=="agent_start" {
                        let message=s.inner.lock().unwrap().store.duplicate(&req.request_id)?;
                        if let Some(mut message)=message {message.status=MessageStatus::Streaming;s.save(message)?;}
                    }
                    transcript.record(s,&e)?;
                    if started && e["type"]=="agent_settled" && !abort_sent {
                        if let Some(e)=&transcript.failure {bail!("{e}");}return Ok(());
                    }
                }
            }
        }
    }.await;
    drop(stdin);
    // Close RPC stdin for orderly disposal. On timeout terminate the entire Pi
    // process group as well; Pi's abort path owns detached bash tool cleanup.
    let exit = match tokio::time::timeout(Duration::from_secs(3), child.wait()).await {
        Ok(status) => status?,
        Err(_) => {
            unsafe {
                libc::kill(-(pid as i32), libc::SIGKILL);
            }
            let _ = child.kill().await;
            let _ = child.wait().await;
            bail!("Pi 未确认停止，请 Quit 后重开环境");
        }
    };
    anyhow::ensure!(
        exit.success(),
        "Pi 未确认停止（异常退出 {exit}），请 Quit 后重开环境"
    );
    if session.exists() {
        std::fs::File::open(&session)?.sync_all()?;
    }
    result
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::response::sse::Event as WireEvent;
    use std::sync::atomic::{AtomicUsize, Ordering};

    #[test]
    fn authoritative_message_and_tool_failures_replace_partial_output() {
        let s = crate::tests::service();
        let mut t = Transcript::default();
        t.record(
            &s,
            &json!({"type":"message_start","message":{"role":"assistant"}}),
        )
        .unwrap();
        t.record(&s,&json!({"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"partial"}})).unwrap();
        t.record(&s,&json!({"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"final"}],"stopReason":"error","errorMessage":"provider offline"}})).unwrap();
        assert_eq!(t.assistant.as_ref().unwrap().status, MessageStatus::Failed);
        assert!(!t.assistant.as_ref().unwrap().text.contains("partial"));
        assert!(t.failure.is_some());
        t.record(&s,&json!({"type":"tool_execution_start","toolCallId":"t1","toolName":"bash","args":{"command":"false"}})).unwrap();
        t.record(&s,&json!({"type":"tool_execution_end","toolCallId":"t1","toolName":"bash","isError":true,"result":{"content":[{"type":"text","text":"exit 1"}]}})).unwrap();
        assert_eq!(t.tools["t1"].status, MessageStatus::Failed);
    }
    #[tokio::test]
    async fn stale_cancel_cannot_stop_a_new_turn() {
        let mut s = crate::tests::service();
        s.pi = Some(Config {
            api_key: "fixture".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 1,
            max_tokens: 128,
            max_requests_per_turn: 1,
        });
        s.turn(Some(TurnState {
            user_message_id: "new".into(),
            cancelling: false,
        }));
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let e = cancel(
            State(s.clone()),
            h,
            Json(CancelTurn {
                request_id: "c1".into(),
                user_message_id: "old".into(),
            }),
        )
        .await
        .unwrap_err();
        assert_eq!(e.0, StatusCode::CONFLICT);
        assert!(!s.snapshot().unwrap().turn.unwrap().cancelling);
    }

    #[tokio::test(flavor = "multi_thread", worker_threads = 2)]
    #[ignore = "Runs installed Pi against a local mock provider; set POT_TEST_PI_BIN"]
    async fn real_pi_tools_resume_cancel_and_budget() {
        let program = std::env::var("POT_TEST_PI_BIN").expect("POT_TEST_PI_BIN required");
        let data = std::env::temp_dir().join(format!("pot-pi-test-{}", id()));
        let workspace = data.join("workspace");
        std::fs::create_dir_all(&workspace).unwrap();
        let calls = Arc::new(AtomicUsize::new(0));
        let captured = Arc::new(Mutex::new(Vec::<Value>::new()));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let port = listener.local_addr().unwrap().port();
        let mut s = crate::tests::service();
        s.data = data.clone();
        s.workspace = workspace.clone();
        s.pi_program = program;
        s.pi = Some(Config {
            api_key: "local-fixture-only".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 5,
            max_tokens: 512,
            max_requests_per_turn: 3,
        });
        s.api_budget = Some(deepseek::BudgetSettings {
            namespace: "pi-attempts",
            data: data.clone(),
            limit: 5,
            max_output_tokens: 512,
        });
        s.pi_upstream = format!("http://127.0.0.1:{port}/mock");
        let (tx, mut rx) = mpsc::unbounded_channel();
        s.commands = tx;
        private_json(
            &data.join(RUNTIME_ENDPOINT_FILE),
            &json!(RuntimeEndpoint {
                api_version: API_VERSION,
                port,
                token: s.token.clone(),
                boot_id: s.boot.clone()
            }),
        )
        .unwrap();
        let count = calls.clone();
        let requests = captured.clone();
        let router=Router::new().route("/api/pi/v1/chat/completions",post(gateway)).with_state(s.clone())
            .route("/mock",post(move |Json(body):Json<Value>| {
                let count=count.clone();let requests=requests.clone();async move {
                    let index=count.fetch_add(1,Ordering::SeqCst);requests.lock().unwrap().push(body);
                    let (delta,reason)=match index {
                        0=>(json!({"role":"assistant","tool_calls":[{"index":0,"id":"write1","type":"function","function":{"name":"write","arguments":json!({"path":"note.txt","content":"APRICOT"}).to_string()}},{"index":1,"id":"bash1","type":"function","function":{"name":"bash","arguments":json!({"command":"cat note.txt"}).to_string()}}]}),"tool_calls"),
                        1=>(json!({"role":"assistant","content":"Created APRICOT."}),"stop"),
                        2=>(json!({"role":"assistant","content":"Remembered APRICOT."}),"stop"),
                        3=>(json!({"role":"assistant","tool_calls":[{"index":0,"id":"slow","type":"function","function":{"name":"bash","arguments":json!({"command":"echo $$ > child.pid; sleep 30; echo BAD > after-cancel.txt","timeout":60}).to_string()}}]}),"tool_calls"),
                        _=>(json!({"role":"assistant","content":"Recovered."}),"stop"),
                    };
                    let chunk=|d:Value,finish:Value|json!({"id":format!("mock-{index}"),"object":"chat.completion.chunk","created":1,"model":"deepseek-v4-flash","choices":[{"index":0,"delta":d,"finish_reason":finish}]}).to_string();
                    let frames=vec![WireEvent::default().data(chunk(delta,Value::Null)),WireEvent::default().data(chunk(json!({}),json!(reason))),WireEvent::default().data(json!({"id":"usage","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}).to_string()),WireEvent::default().data("[DONE]")];
                    Sse::new(futures::stream::iter(frames.into_iter().map(Ok::<_,Infallible>)))
                }
            }));
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let submit = |n: &str| SendMessage {
            request_id: n.into(),
            conversation_id: "main".into(),
            text: format!("Task {n}; remember APRICOT"),
        };
        for n in ["first", "resume"] {
            let _ = send(State(s.clone()), h.clone(), Json(submit(n)))
                .await
                .unwrap();
            let req = rx.recv().await.unwrap();
            tokio::time::timeout(Duration::from_secs(30), run(&s, req))
                .await
                .unwrap()
                .unwrap();
            s.turn(None);
            assert!(
                !s.snapshot()
                    .unwrap()
                    .messages
                    .iter()
                    .any(|m| m.role == Role::System),
                "{:?}",
                s.snapshot().unwrap().messages
            );
        }
        assert_eq!(
            std::fs::read_to_string(workspace.join("note.txt")).unwrap(),
            "APRICOT"
        );
        assert!(data.join("pi-session.jsonl").exists());
        let requests = captured.lock().unwrap();
        assert_eq!(requests.len(), 3);
        assert!(
            requests[2]["messages"]
                .to_string()
                .contains("Created APRICOT")
        );
        assert!(
            requests
                .iter()
                .all(|r| r["max_tokens"] == 512 && r["thinking"]["type"] == "disabled")
        );
        drop(requests);
        let _ = send(State(s.clone()), h.clone(), Json(submit("cancel")))
            .await
            .unwrap();
        let req = rx.recv().await.unwrap();
        let worker = s.clone();
        let job = tokio::spawn(async move { run(&worker, req).await });
        tokio::time::timeout(Duration::from_secs(30), async {
            while !workspace.join("child.pid").exists() {
                tokio::time::sleep(Duration::from_millis(50)).await;
            }
        })
        .await
        .unwrap();
        let _ = cancel(
            State(s.clone()),
            h.clone(),
            Json(CancelTurn {
                request_id: "cancel-command".into(),
                user_message_id: s.snapshot().unwrap().turn.unwrap().user_message_id,
            }),
        )
        .await
        .unwrap();
        tokio::time::timeout(Duration::from_secs(15), job)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        s.turn(None);
        assert!(!workspace.join("after-cancel.txt").exists());
        let pid: i32 = std::fs::read_to_string(workspace.join("child.pid"))
            .unwrap()
            .trim()
            .parse()
            .unwrap();
        assert_ne!(
            unsafe { libc::kill(pid, 0) },
            0,
            "Bash child survived cancellation"
        );
        assert_eq!(
            s.inner
                .lock()
                .unwrap()
                .store
                .duplicate("cancel")
                .unwrap()
                .unwrap()
                .status,
            MessageStatus::Cancelled
        );
        assert_eq!(
            calls.load(Ordering::SeqCst),
            4,
            "No retry after cancellation"
        );
        let _ = send(State(s.clone()), h.clone(), Json(submit("recover")))
            .await
            .unwrap();
        run(&s, rx.recv().await.unwrap()).await.unwrap();
        s.turn(None);
        assert_eq!(calls.load(Ordering::SeqCst), 5);
        let before = s.snapshot().unwrap();
        assert_eq!(
            send(State(s.clone()), h, Json(submit("over-budget")))
                .await
                .unwrap_err()
                .0,
            StatusCode::TOO_MANY_REQUESTS
        );
        assert_eq!(s.snapshot().unwrap(), before);
        assert_eq!(s.budget().unwrap().unwrap().used, 5);
        // A missing native session must not silently create a replacement or
        // spend another request, even though there is budget for a new prompt.
        s.api_budget.as_mut().unwrap().limit = 6;
        std::fs::rename(
            data.join("pi-session.jsonl"),
            data.join("saved-session.jsonl"),
        )
        .unwrap();
        let _ = send(
            State(s.clone()),
            {
                let mut h = HeaderMap::new();
                h.insert("authorization", "Bearer test-token".parse().unwrap());
                h
            },
            Json(submit("missing-session")),
        )
        .await
        .unwrap();
        run(&s, rx.recv().await.unwrap()).await.unwrap();
        s.turn(None);
        assert_eq!(calls.load(Ordering::SeqCst), 5);
        assert!(!data.join("pi-session.jsonl").exists());
        assert_eq!(
            s.inner
                .lock()
                .unwrap()
                .store
                .duplicate("missing-session")
                .unwrap()
                .unwrap()
                .status,
            MessageStatus::Failed
        );
        server.abort();
        std::fs::remove_dir_all(data).unwrap();
    }
}
