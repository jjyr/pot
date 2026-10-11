use super::*;
use futures::{StreamExt, future::BoxFuture, stream::BoxStream};
use serde_json::{Value, json};
use std::{
    path::PathBuf,
    sync::atomic::{AtomicUsize, Ordering},
    time::Duration,
};

struct Config;
impl ProfileConfig for Config {
    fn model(&self) -> &str {
        "fixture"
    }
    fn max_tokens(&self) -> u64 {
        128
    }
}
#[derive(Default)]
struct Feedback {
    context: Mutex<Option<Value>>,
    diagnostics: Mutex<Vec<Value>>,
    settlement: Option<Arc<Semaphore>>,
}
impl ExecutionFeedback for Feedback {
    fn settle(&self) -> BoxFuture<'_, Result<()>> {
        Box::pin(async move {
            if let Some(gate) = &self.settlement {
                gate.acquire().await?.forget();
                anyhow::bail!("Unconfirmed receipt");
            }
            Ok(())
        })
    }
    fn cancelled(&self) -> bool {
        false
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        Box::pin(std::future::pending())
    }
    fn record_turn(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn input_streaming(&self, _: &str) -> Result<()> {
        Ok(())
    }
    fn load_context(&self) -> Result<Option<Value>> {
        Ok(self.context.lock().unwrap().clone())
    }
    fn save_context(&self, value: &Value) -> Result<()> {
        *self.context.lock().unwrap() = Some(value.clone());
        Ok(())
    }
    fn diagnostic(&self, value: &Value) -> Result<()> {
        self.diagnostics.lock().unwrap().push(value.clone());
        Ok(())
    }
}
struct Model {
    started: AtomicUsize,
    gate: Semaphore,
}
impl Model {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            started: AtomicUsize::new(0),
            gate: Semaphore::new(0),
        })
    }
}
impl ExecutionServices for Model {
    fn model(&self, _: ModelRequest) -> BoxFuture<'_, Result<BoxStream<'_, Result<Vec<u8>>>>> {
        Box::pin(async move {
            self.started.fetch_add(1, Ordering::SeqCst);
            self.gate.acquire().await?.forget();
            Ok(futures::stream::iter([Ok(b"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n".to_vec())]).boxed())
        })
    }
    fn rpc<'a>(
        &'a self,
        _: Option<&'a str>,
        _: &'a str,
        _: &'a str,
        _: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        Box::pin(async { Ok((json!({"tools":[]}), Some("fixture-session".into()))) })
    }
    fn compaction(&self, _: Value) -> BoxFuture<'_, Result<()>> {
        Box::pin(async { Ok(()) })
    }
}
struct Cleanup {
    aborted: AtomicUsize,
    failure: bool,
    started: AtomicUsize,
    gate: Semaphore,
}
impl ToolExecutor for Cleanup {
    fn execute<'a>(
        &'a self,
        _: &'a ExecutionContext,
        _: &'a ShellSettings,
        _: &'a str,
        _: &'a Value,
        _: &'a str,
        _: &'a mut (dyn FnMut(Update) -> Result<()> + Send),
    ) -> BoxFuture<'a, Result<Value>> {
        Box::pin(async { anyhow::bail!("Unexpected tool") })
    }
    fn abort<'a>(&'a self, context: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        self.aborted.fetch_add(1, Ordering::SeqCst);
        self.finish(context)
    }
    fn finish<'a>(&'a self, _: &'a ExecutionContext) -> BoxFuture<'a, Result<()>> {
        Box::pin(async move {
            self.started.fetch_add(1, Ordering::SeqCst);
            self.gate.acquire().await?.forget();
            anyhow::ensure!(!self.failure, "Cleanup unconfirmed");
            Ok(())
        })
    }
}
struct Fixture {
    data: PathBuf,
    manager: Manager,
    model: Arc<Model>,
}
impl Fixture {
    fn new() -> Self {
        let data = std::env::temp_dir().join(format!("pot-execution-{}", uuid::Uuid::new_v4()));
        let manager = Manager::open(
            &data,
            "runtime",
            "boot",
            Limits {
                max_active_agents: 1,
                max_queued_agents: 2,
            },
        )
        .unwrap();
        Self {
            data,
            manager,
            model: Model::new(),
        }
    }
    fn start(
        &self,
        owner: &str,
        role: ExecutionRole,
        tools: Option<Arc<dyn ToolExecutor>>,
    ) -> tokio::task::JoinHandle<Result<Outcome>> {
        self.start_with_feedback(owner, role, tools, Arc::new(Feedback::default()))
    }
    fn start_with_feedback(
        &self,
        owner: &str,
        role: ExecutionRole,
        tools: Option<Arc<dyn ToolExecutor>>,
        feedback: Arc<Feedback>,
    ) -> tokio::task::JoinHandle<Result<Outcome>> {
        let (manager, model, data, owner) = (
            self.manager.clone(),
            self.model.clone(),
            self.data.clone(),
            owner.to_string(),
        );
        tokio::spawn(async move {
            let mut profile = Profile::api(&Config);
            profile.params.agent.auto_compact = false;
            profile.params.agent.builtin_tools.clear();
            profile.params.agent.system_prompt = Some("PRIVATE_SYSTEM_PROMPT".into());
            let config = profile.resolve()?;
            let context = ExecutionContext {
                instructions: String::new(),
                skill_resources: Default::default(),
                data: data.clone(),
                workspace: data,
                owner: owner.clone(),
                role,
                main_prompt: String::new(),
                feedback: feedback.clone(),
                tool_executor: tools,
            };
            let result = manager
                .execute(
                    Invocation {
                        context: &context,
                        thread: &Thread {
                            owner,
                            generation: 1,
                            config,
                        },
                        request: &TurnRequest {
                            request_id: "turn".into(),
                            text: "PRIVATE_USER_PROMPT".into(),
                        },
                        resuming: false,
                        services: model,
                        extensions: None,
                    },
                    &mut |_| Ok(()),
                )
                .await;
            for event in feedback.diagnostics.lock().unwrap().iter() {
                assert!(event["execution_id"].as_str().is_some());
            }
            result
        })
    }
    async fn phase(&self, owner: &str, phase: Phase) -> String {
        tokio::time::timeout(Duration::from_secs(5), async {
            loop {
                if let Some(e) = self
                    .manager
                    .snapshot()
                    .into_iter()
                    .find(|e| e.owner == owner && e.phase == phase)
                {
                    return e.execution_id;
                }
                tokio::task::yield_now().await;
            }
        })
        .await
        .unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.manager.stop();
        let _ = std::fs::remove_dir_all(&self.data);
    }
}
async fn count(counter: &AtomicUsize, n: usize) {
    tokio::time::timeout(Duration::from_secs(5), async {
        while counter.load(Ordering::SeqCst) != n {
            tokio::task::yield_now().await;
        }
    })
    .await
    .unwrap();
}

#[tokio::test]
async fn assistants_and_tasks_share_capacity_and_cancel_without_starting_queued_agent() {
    let f = Fixture::new();
    let assistant = f.start("assistant", ExecutionRole::Main, None);
    count(&f.model.started, 1).await;
    let task = f.start("task", ExecutionRole::Worker, None);
    let task_id = f.phase("task", Phase::Queued).await;
    assert!(f.manager.cancel(&task_id));
    assert_eq!(task.await.unwrap().unwrap(), Outcome::Cancelled);
    assert_eq!(f.model.started.load(Ordering::SeqCst), 1);
    f.model.gate.add_permits(1);
    assert_eq!(assistant.await.unwrap().unwrap(), Outcome::Success);
    f.manager.drain().await.unwrap();
    let log = std::fs::read_to_string(f.data.join("runtime-executions.jsonl")).unwrap();
    assert!(!log.contains("PRIVATE_"));
    assert!(log.contains(&task_id));
    assert!(log.contains("succeeded"));
    assert!(log.contains("cancelled"));
}
#[tokio::test]
async fn running_cancel_keeps_capacity_until_cleanup_completes() {
    let f = Fixture::new();
    let cleanup = Arc::new(Cleanup {
        aborted: AtomicUsize::new(0),
        failure: false,
        started: AtomicUsize::new(0),
        gate: Semaphore::new(0),
    });
    let first = f.start("first", ExecutionRole::Worker, Some(cleanup.clone()));
    count(&f.model.started, 1).await;
    let second = f.start("second", ExecutionRole::Main, None);
    f.phase("second", Phase::Queued).await;
    f.manager.cancel(&f.phase("first", Phase::Running).await);
    count(&cleanup.started, 1).await;
    assert_eq!(cleanup.aborted.load(Ordering::SeqCst), 1);
    assert_eq!(f.model.started.load(Ordering::SeqCst), 1);
    assert!(!first.is_finished());
    cleanup.gate.add_permits(1);
    assert_eq!(first.await.unwrap().unwrap(), Outcome::Cancelled);
    count(&f.model.started, 2).await;
    f.model.gate.add_permits(1);
    assert_eq!(second.await.unwrap().unwrap(), Outcome::Success);
}
#[tokio::test]
async fn stop_cancels_running_and_queued_and_rejects_new_work() {
    let f = Fixture::new();
    let running = f.start("running", ExecutionRole::Worker, None);
    count(&f.model.started, 1).await;
    let queued = f.start("queued", ExecutionRole::Main, None);
    f.phase("queued", Phase::Queued).await;
    f.manager.stop();
    assert_eq!(running.await.unwrap().unwrap(), Outcome::Cancelled);
    assert_eq!(queued.await.unwrap().unwrap(), Outcome::Cancelled);
    assert!(
        f.start("new", ExecutionRole::Main, None)
            .await
            .unwrap()
            .is_err()
    );
    f.manager.drain().await.unwrap();
}
#[tokio::test]
async fn duplicate_and_full_queue_are_rejected_and_dropped_execution_is_fenced() {
    let f = Fixture::new();
    let running = f.start("running", ExecutionRole::Worker, None);
    count(&f.model.started, 1).await;
    assert!(
        f.start("running", ExecutionRole::Worker, None)
            .await
            .unwrap()
            .unwrap_err()
            .to_string()
            .contains("already active")
    );
    let queued = f.start("queued", ExecutionRole::Worker, None);
    f.phase("queued", Phase::Queued).await;
    let queued2 = f.start("queued2", ExecutionRole::Main, None);
    f.phase("queued2", Phase::Queued).await;
    assert!(
        f.start("overflow", ExecutionRole::Worker, None)
            .await
            .unwrap()
            .unwrap_err()
            .to_string()
            .contains("queue is full")
    );
    running.abort();
    let _ = running.await;
    f.phase("running", Phase::Interrupted).await;
    assert_eq!(f.model.started.load(Ordering::SeqCst), 1);
    assert!(f.manager.drain().await.is_err());
    f.manager.stop();
    assert_eq!(queued.await.unwrap().unwrap(), Outcome::Cancelled);
    assert_eq!(queued2.await.unwrap().unwrap(), Outcome::Cancelled);
}
#[test]
fn limits_are_explicit_and_invalid_values_are_rejected() {
    let f = Fixture::new();
    assert!(
        Manager::open(
            &f.data,
            "r",
            "b",
            Limits {
                max_active_agents: 0,
                max_queued_agents: 1
            }
        )
        .is_err()
    );
    std::fs::write(
        f.data.join("runtime-execution.json"),
        json!({"max_parallel_tasks":4}).to_string(),
    )
    .unwrap();
    assert!(Limits::load(&f.data).is_err());
}

#[test]
fn late_duplicate_and_post_terminal_events_are_isolated() {
    let mut gate = EventGate::new(2, "turn");
    let mut event = AgentEvent {
        generation: 1,
        turn_id: "turn".into(),
        seq: 1,
        update: Update::TextDelta("old".into()),
    };
    assert!(!gate.accept(&event));
    event.generation = 2;
    event.turn_id = "other".into();
    assert!(!gate.accept(&event));
    event.turn_id = "turn".into();
    assert!(gate.accept(&event));
    assert!(!gate.accept(&event));
    event.seq = 2;
    event.update = Update::TurnFinished(Outcome::Cancelled);
    assert!(gate.accept(&event));
    event.seq = 3;
    assert!(!gate.accept(&event));
    assert_eq!(gate.finish(), Outcome::Cancelled);
    assert_eq!(EventGate::new(1, "missing").finish(), Outcome::Unknown);
}

#[tokio::test]
async fn settlement_holds_slot_and_unknown_result_never_admits_another_agent() {
    let f = Fixture::new();
    let gate = Arc::new(Semaphore::new(0));
    let first = f.start_with_feedback(
        "first",
        ExecutionRole::Worker,
        None,
        Arc::new(Feedback {
            settlement: Some(gate.clone()),
            ..Default::default()
        }),
    );
    count(&f.model.started, 1).await;
    let second = f.start("second", ExecutionRole::Main, None);
    f.phase("second", Phase::Queued).await;
    f.model.gate.add_permits(1);
    // No terminal admission while settlement is waiting.
    assert!(!first.is_finished());
    assert_eq!(f.model.started.load(Ordering::SeqCst), 1);
    gate.add_permits(1);
    assert_eq!(first.await.unwrap().unwrap(), Outcome::Unknown);
    f.phase("first", Phase::Interrupted).await;
    assert_eq!(f.model.started.load(Ordering::SeqCst), 1);
    f.manager.stop();
    assert_eq!(second.await.unwrap().unwrap(), Outcome::Cancelled);
}

#[test]
fn logs_are_lazy_private_and_rotate_without_payloads() {
    use std::os::unix::fs::PermissionsExt;
    let f = Fixture::new();
    let path = f.data.join("runtime-executions.jsonl");
    assert!(!path.exists());
    let execution = Execution {
        execution_id: "id".into(),
        runtime_id: "r".into(),
        boot_id: "b".into(),
        owner: "o".into(),
        request_id: "t".into(),
        generation: 1,
        role: "task".into(),
        phase: Phase::Queued,
    };
    std::fs::write(&path, vec![b'x'; 8 * 1024 * 1024]).unwrap();
    f.manager.log(&execution, "queued", None);
    assert!(path.with_extension("jsonl.1").exists());
    assert_eq!(
        std::fs::metadata(&path).unwrap().permissions().mode() & 0o777,
        0o600
    );
    let value: Value = serde_json::from_str(&std::fs::read_to_string(path).unwrap()).unwrap();
    assert_eq!(value["execution"]["execution_id"], "id");
}

#[tokio::test]
async fn failed_tool_cleanup_is_unknown_and_retains_capacity() {
    let f = Fixture::new();
    let cleanup = Arc::new(Cleanup {
        aborted: AtomicUsize::new(0),
        failure: true,
        started: AtomicUsize::new(0),
        gate: Semaphore::new(1),
    });
    let first = f.start("first", ExecutionRole::Worker, Some(cleanup));
    count(&f.model.started, 1).await;
    f.model.gate.add_permits(1);
    assert_eq!(first.await.unwrap().unwrap(), Outcome::Unknown);
    f.phase("first", Phase::Interrupted).await;
    assert!(f.manager.drain().await.is_err());
}
