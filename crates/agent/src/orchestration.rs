//! Turn-scoped, capability-only JavaScript composition. No OS or network bindings.
use anyhow::{Result, anyhow, ensure};
use futures::{StreamExt, future::BoxFuture, stream::FuturesUnordered};
use pot_core::Update;
use rquickjs::{Context, Function, Runtime};
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, AtomicU64, AtomicUsize, Ordering},
    },
    time::{Duration, Instant},
};
use tokio::sync::mpsc;

const OUTPUT_LIMIT: usize = 65536;
const TIME_LIMIT: Duration = Duration::from_secs(600);
pub type Events = Arc<Mutex<Vec<Update>>>;
pub type Dispatch<'a> =
    Arc<dyn Fn(String, Value, String, Events) -> BoxFuture<'a, Result<Value>> + Send + Sync + 'a>;

pub fn schemas() -> Vec<Value> {
    vec![
        json!({"name":"exec","description":"Compose available tools using JavaScript with top-level await. tools.NAME(args) returns the same JSON result as a direct call and enforces the same role, execution target and approvals. Use Promise.all for independent calls; publish only needed results using text(value). No filesystem, network, process, import, timers or Node globals. Child results remain private unless text publishes them. Await all effectful calls. Unawaited calls may already have effects. Returns output and, if still running, exec_id for wait. Runs are scoped to this turn and cancelled when the turn ends. Limits: 4 runs, 128 calls/run, 8 simultaneous calls/run, 64 MiB JS memory, 64 KiB total output, 2 seconds uninterrupted JavaScript execution, 10 minutes wall time. Recursive exec/wait is unavailable.","inputSchema":{"type":"object","properties":{"code":{"type":"string","maxLength":65536},"yield_time_ms":{"type":"integer","minimum":1,"maximum":60000}},"required":["code"],"additionalProperties":false}}),
        json!({"name":"wait","description":"Collect new explicit output or completion from a running exec in this turn. Returns only output since the previous exec/wait response. Does not poll shell sessions or background Tasks; use write_stdin for process sessions. Unknown or expired exec IDs fail.","inputSchema":{"type":"object","properties":{"exec_id":{"type":"string"},"yield_time_ms":{"type":"integer","minimum":1,"maximum":60000}},"required":["exec_id"],"additionalProperties":false}}),
    ]
}
#[derive(Default)]
struct State {
    output: String,
    error: Option<String>,
    done: bool,
}
struct Job {
    state: Arc<Mutex<State>>,
    stop: Arc<AtomicBool>,
}
impl Drop for Job {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Relaxed);
    }
}
pub struct Manager<'a> {
    jobs: HashMap<String, Job>,
    running: FuturesUnordered<BoxFuture<'a, ()>>,
    dispatch: Dispatch<'a>,
    names: Vec<String>,
    pub events: Events,
}
impl<'a> Manager<'a> {
    pub fn new(dispatch: Dispatch<'a>, names: Vec<String>) -> Self {
        Self {
            jobs: HashMap::new(),
            running: FuturesUnordered::new(),
            dispatch,
            names,
            events: Arc::default(),
        }
    }
    /// Drive child calls even while the model or an ordinary tool is running.
    pub async fn drive<T>(&mut self, future: impl std::future::Future<Output = T>) -> T {
        tokio::pin!(future);
        loop {
            tokio::select! {
                result = &mut future => return result,
                _ = self.running.next(), if !self.running.is_empty() => {}
            }
        }
    }
    pub async fn call(&mut self, name: &str, args: &Value, call_id: &str) -> Result<Value> {
        let fields = if name == "exec" {
            ["code", "yield_time_ms"]
        } else {
            ["exec_id", "yield_time_ms"]
        };
        ensure!(
            args.as_object()
                .is_some_and(|o| o.keys().all(|k| fields.contains(&k.as_str()))),
            "Invalid orchestration arguments"
        );
        let delay = args
            .get("yield_time_ms")
            .map(|v| {
                v.as_u64()
                    .filter(|n| (1..=60000).contains(n))
                    .ok_or_else(|| anyhow!("Invalid yield_time_ms"))
            })
            .transpose()?
            .unwrap_or(1000);
        let id = if name == "exec" {
            let code = args["code"]
                .as_str()
                .filter(|s| !s.is_empty() && s.len() <= 65536)
                .ok_or_else(|| anyhow!("exec requires code up to 64 KiB"))?;
            ensure!(self.jobs.len() < 4, "exec run limit reached for this turn");
            let id = uuid::Uuid::new_v4().to_string();
            let state = Arc::new(Mutex::new(State::default()));
            let stop = Arc::new(AtomicBool::new(false));
            let (tx, rx) = mpsc::unbounded_channel();
            let (reply, replies) = std::sync::mpsc::channel();
            let thread_stop = stop.clone();
            let names = self.names.clone();
            let source = code.to_owned();
            std::thread::Builder::new()
                .name("pot-js".into())
                .spawn(move || {
                    let result = javascript(source, names, thread_stop, tx.clone(), replies);
                    let _ = tx.send(Event::Done(result.err().map(|e| format!("{e:#}"))));
                })?;
            self.running.push(Box::pin(run(
                rx,
                reply,
                state.clone(),
                stop.clone(),
                self.dispatch.clone(),
                self.events.clone(),
                call_id.to_owned(),
            )));
            self.jobs.insert(id.clone(), Job { state, stop });
            id
        } else {
            args["exec_id"]
                .as_str()
                .ok_or_else(|| anyhow!("wait requires exec_id"))?
                .to_owned()
        };
        let state = self
            .jobs
            .get(&id)
            .ok_or_else(|| anyhow!("Unknown or expired exec_id"))?
            .state
            .clone();
        let deadline = tokio::time::sleep(Duration::from_millis(delay));
        tokio::pin!(deadline);
        loop {
            {
                let mut s = state.lock().unwrap();
                if s.done || !s.output.is_empty() || deadline.is_elapsed() {
                    let mut value =
                        json!({"output":std::mem::take(&mut s.output),"completed":s.done});
                    if !s.done {
                        value["exec_id"] = json!(id);
                    }
                    if let Some(error) = &s.error {
                        value["isError"] = json!(true);
                        value["error"] = json!(error);
                    }
                    return Ok(value);
                }
            }
            tokio::select! {
                _ = &mut deadline => {},
                _ = self.running.next(), if !self.running.is_empty() => {}
            }
        }
    }
    pub fn drain(&self, emit: &mut (dyn FnMut(Update) -> Result<()> + Send)) -> Result<()> {
        for update in std::mem::take(&mut *self.events.lock().unwrap()) {
            emit(update)?;
        }
        Ok(())
    }
}

enum Event {
    Call(u32, String, String),
    Text(String),
    Done(Option<String>),
}
async fn run<'a>(
    mut rx: mpsc::UnboundedReceiver<Event>,
    reply: std::sync::mpsc::Sender<(u32, bool, String)>,
    state: Arc<Mutex<State>>,
    stop: Arc<AtomicBool>,
    dispatch: Dispatch<'a>,
    events: Events,
    parent: String,
) {
    let mut calls = FuturesUnordered::new();
    let deadline = tokio::time::sleep(TIME_LIMIT);
    tokio::pin!(deadline);
    let mut bytes = 0;
    let mut count = 0;
    let error = loop {
        tokio::select! {
            _ = &mut deadline => break Some("exec exceeded 10 minute wall time".into()),
            event = rx.recv() => match event {
                Some(Event::Call(id, name, args)) => {
                    count += 1;
                    if count > 128 || calls.len() >= 8 {
                        let _ = reply.send((id, false, json!("exec call/concurrency limit reached").to_string()));
                        continue;
                    }
                    let dispatch = dispatch.clone(); let events = events.clone(); let child = format!("{parent}:js:{id}");
                    calls.push(async move {
                        let value = match serde_json::from_str(&args) {
                            Ok(args) => dispatch(name, args, child, events).await,
                            Err(error) => Err(error.into()),
                        };
                        (id, value)
                    });
                },
                Some(Event::Text(text)) => {
                    bytes += text.len() + 1;
                    if bytes > OUTPUT_LIMIT { break Some("exec output exceeds 64 KiB".into()); }
                    let mut s = state.lock().unwrap(); s.output.push_str(&text); s.output.push('\n');
                },
                Some(Event::Done(error)) => break error,
                None => break Some("JavaScript worker stopped".into()),
            },
            result = calls.next(), if !calls.is_empty() => {
                if let Some((id, result)) = result {
                    if let Err(error) = &result && error.to_string().contains("stop unconfirmed") {
                        break Some(error.to_string());
                    }
                    let (ok, value) = match result { Ok(v) => (true, v), Err(e) => (false, json!(e.to_string())) };
                    let _ = reply.send((id, ok, value.to_string()));
                }
            }
        }
    };
    stop.store(true, Ordering::Relaxed);
    let mut s = state.lock().unwrap();
    s.done = true;
    s.error = error;
}
fn javascript(
    code: String,
    names: Vec<String>,
    stop: Arc<AtomicBool>,
    tx: mpsc::UnboundedSender<Event>,
    replies: std::sync::mpsc::Receiver<(u32, bool, String)>,
) -> Result<()> {
    let rt = Runtime::new()?;
    rt.set_memory_limit(64 * 1024 * 1024);
    rt.set_max_stack_size(512 * 1024);
    let started = Instant::now();
    let deadline = started + TIME_LIMIT;
    let quantum = Arc::new(AtomicU64::new(2000));
    let cpu = quantum.clone();
    let interrupt = stop.clone();
    rt.set_interrupt_handler(Some(Box::new(move || {
        interrupt.load(Ordering::Relaxed)
            || Instant::now() >= deadline
            || started.elapsed().as_millis() as u64 >= cpu.load(Ordering::Relaxed)
    })));
    let context = Context::full(&rt)?;
    let done = Arc::new(AtomicBool::new(false));
    context.with(|ctx| -> Result<()> {
        let sender = tx.clone();
        ctx.globals().set("__send", Function::new(ctx.clone(), move |id: u32, name: String, args: String| -> rquickjs::Result<()> {
            if args.len() > 4 * 1024 * 1024 { return Err(rquickjs::Error::new_from_js_message("string", "tool arguments", "Arguments exceed 4 MiB")); }
            sender.send(Event::Call(id, name, args)).map_err(|_| rquickjs::Error::new_from_js_message("call", "tool", "exec stopped"))
        })?)?;
        let sender = tx.clone();
        let output_bytes = AtomicUsize::new(0);
        ctx.globals().set("__text", Function::new(ctx.clone(), move |text: String| -> rquickjs::Result<()> {
            if output_bytes.fetch_add(text.len() + 1, Ordering::Relaxed) + text.len() + 1 > OUTPUT_LIMIT { return Err(rquickjs::Error::new_from_js_message("string", "output", "Output exceeds 64 KiB")); }
            sender.send(Event::Text(text)).map_err(|_| rquickjs::Error::new_from_js_message("text", "output", "exec stopped"))
        })?)?;
        let finished = done.clone(); let sender = tx.clone();
        ctx.globals().set("__finish", Function::new(ctx.clone(), move |error: Option<String>| {
            finished.store(true, Ordering::Relaxed); let _ = sender.send(Event::Done(error));
        })?)?;
        ctx.eval::<(), _>(format!(r#"
        (() => {{
          const send = globalThis.__send, publish = globalThis.__text, finish = globalThis.__finish;
          delete globalThis.__send; delete globalThis.__text; delete globalThis.__finish;
          const pending = new Map(); let count = 0;
          globalThis.__deliver = (id, ok, value) => {{
            const entry = pending.get(id); if (!entry) return;
            pending.delete(id); ok ? entry.resolve(value) : entry.reject(new Error(value));
          }};
          const tools = Object.create(null);
          for (const name of {names}) tools[name] = (args = {{}}) => {{
            if (count >= 128 || pending.size >= 8) return Promise.reject(new Error('exec call/concurrency limit reached'));
            const data = JSON.stringify(args); const id = ++count;
            return new Promise((resolve,reject) => {{ pending.set(id,{{resolve,reject}}); try {{ send(id,name,data); }} catch(e) {{ pending.delete(id); reject(e); }} }});
          }};
          Object.freeze(tools);
          const text = value => publish(typeof value === 'string' ? value : JSON.stringify(value));
          const AsyncFunction = Object.getPrototypeOf(async function(){{}}).constructor;
          new AsyncFunction('tools','text',{code})(tools,text).then(() => finish(null), e => finish(String(e)));
        }})();
        "#, names=serde_json::to_string(&names)?, code=serde_json::to_string(&code)?))
        .map_err(|e| anyhow!("JavaScript initialization: {e}; {:?}", ctx.catch()))?;
        Ok(())
    })?;
    loop {
        if stop.load(Ordering::Relaxed) {
            break;
        }
        quantum.store(
            started.elapsed().as_millis() as u64 + 2000,
            Ordering::Relaxed,
        );
        while rt
            .execute_pending_job()
            .map_err(|e| anyhow!("JavaScript job failed: {e:?}"))?
        {
            if done.load(Ordering::Relaxed) || stop.load(Ordering::Relaxed) {
                break;
            }
        }
        if done.load(Ordering::Relaxed) {
            break;
        }
        if Instant::now() >= deadline {
            anyhow::bail!("exec exceeded 10 minute wall time");
        }
        match replies.recv_timeout(Duration::from_millis(10)) {
            Ok((id, ok, value)) => context.with(|ctx| -> Result<()> {
                quantum.store(
                    started.elapsed().as_millis() as u64 + 2000,
                    Ordering::Relaxed,
                );
                ctx.eval::<(), _>(format!("__deliver({id},{ok},{value});"))?;
                Ok(())
            })?,
            Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {}
            Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => break,
        }
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    async fn finish(manager: &mut Manager<'_>, code: &str) -> Value {
        tokio::time::timeout(Duration::from_secs(5), async {
            let mut value = manager
                .call("exec", &json!({"code":code,"yield_time_ms":10}), "root")
                .await
                .unwrap();
            let mut output = value["output"].as_str().unwrap().to_owned();
            while value["completed"] != true {
                value = manager
                    .call(
                        "wait",
                        &json!({"exec_id":value["exec_id"],"yield_time_ms":100}),
                        "poll",
                    )
                    .await
                    .unwrap();
                output.push_str(value["output"].as_str().unwrap());
            }
            value["output"] = json!(output);
            value
        })
        .await
        .expect("JavaScript did not settle")
    }
    fn no_tools() -> Manager<'static> {
        Manager::new(
            Arc::new(|_, _, _, _| Box::pin(async { anyhow::bail!("unexpected dispatch") })),
            vec![],
        )
    }
    #[tokio::test]
    async fn parallel_calls_publish_only_selected_results_and_unique_child_ids() {
        let barrier = Arc::new(tokio::sync::Barrier::new(2));
        let ids = Arc::new(Mutex::new(vec![]));
        let observed = ids.clone();
        let mut manager = Manager::new(
            Arc::new(move |name, args, id, _| {
                let barrier = barrier.clone();
                let ids = observed.clone();
                Box::pin(async move {
                    assert_eq!(name, "probe");
                    ids.lock().unwrap().push(id);
                    barrier.wait().await;
                    Ok(json!({"value":args["value"],"private":"LARGE_PRIVATE_RESULT"}))
                })
            }),
            vec!["probe".into()],
        );
        let result = finish(&mut manager, "const r = await Promise.all([tools.probe({value:2}),tools.probe({value:3})]); text(r.reduce((n,r)=>n+r.value,0));").await;
        assert_eq!(result["output"], "5\n");
        assert!(!result.to_string().contains("LARGE_PRIVATE_RESULT"));
        let ids = ids.lock().unwrap();
        assert_eq!(ids.len(), 2);
        assert_ne!(ids[0], ids[1]);
    }
    #[tokio::test]
    async fn wait_drives_calls_during_model_work_and_never_repeats_output() {
        let mut manager = Manager::new(
            Arc::new(|_, _, _, _| {
                Box::pin(async {
                    tokio::time::sleep(Duration::from_millis(80)).await;
                    Ok(json!("after"))
                })
            }),
            vec!["probe".into()],
        );
        let first = manager
            .call(
                "exec",
                &json!({"code":"text('before'); text(await tools.probe({}));","yield_time_ms":10}),
                "root",
            )
            .await
            .unwrap();
        let id = first["exec_id"].clone();
        assert_eq!(first["completed"], false);
        manager
            .drive(tokio::time::sleep(Duration::from_millis(120)))
            .await;
        let final_output = manager
            .call("wait", &json!({"exec_id":id}), "poll")
            .await
            .unwrap();
        assert_eq!(final_output["completed"], true);
        let output = format!(
            "{}{}",
            first["output"].as_str().unwrap(),
            final_output["output"].as_str().unwrap()
        );
        assert_eq!(output, "before\nafter\n");
        assert_eq!(
            manager
                .call("wait", &json!({"exec_id":id}), "again")
                .await
                .unwrap()["output"],
            ""
        );
        assert!(
            manager
                .call("wait", &json!({"exec_id":"other-turn"}), "invalid")
                .await
                .is_err()
        );
    }
    #[tokio::test]
    async fn isolate_has_no_os_network_or_recursive_tools_and_errors_are_visible() {
        let mut manager = no_tools();
        let result = finish(&mut manager,"text([typeof process,typeof require,typeof fetch,typeof tools.exec,typeof tools.wait]);").await;
        assert_eq!(
            result["output"],
            "[\"undefined\",\"undefined\",\"undefined\",\"undefined\",\"undefined\"]\n"
        );
        let result = finish(&mut manager, "await tools.read_file({path:'/etc/passwd'});").await;
        assert_eq!(result["isError"], true);
        let result = finish(&mut manager, "throw new Error('observable failure');").await;
        assert!(
            result["error"]
                .as_str()
                .unwrap()
                .contains("observable failure")
        );
    }
    #[tokio::test]
    async fn output_call_limits_and_drop_stop_the_worker() {
        let mut manager = no_tools();
        assert_eq!(
            finish(&mut manager, "text('x'.repeat(65537));").await["isError"],
            true
        );
        let mut manager = Manager::new(
            Arc::new(|_, _, _, _| Box::pin(async { Ok(json!(null)) })),
            vec!["probe".into()],
        );
        let result = finish(
            &mut manager,
            "for(let i=0;i<129;i++) await tools.probe({});",
        )
        .await;
        assert!(result["error"].as_str().unwrap().contains("limit"));
        let run = manager
            .call(
                "exec",
                &json!({"code":"await new Promise(()=>{});","yield_time_ms":1}),
                "waiting",
            )
            .await
            .unwrap();
        let stop = manager.jobs[run["exec_id"].as_str().unwrap()].stop.clone();
        drop(manager);
        assert!(stop.load(Ordering::Relaxed));
    }
    #[tokio::test]
    async fn runaway_javascript_is_interrupted() {
        let mut manager = no_tools();
        let result = finish(&mut manager, "while(true) {}").await;
        assert_eq!(result["isError"], true);
    }
}

#[cfg(test)]
mod cleanup_tests {
    use super::*;
    #[tokio::test]
    async fn scripts_cannot_hide_unconfirmed_process_cleanup() {
        let mut manager = Manager::new(
            Arc::new(|_, _, _, _| Box::pin(async { anyhow::bail!("stop unconfirmed") })),
            vec!["probe".into()],
        );
        let mut result = manager.call("exec",&json!({"code":"try { await tools.probe({}); } catch(e) { text('ignored'); }","yield_time_ms":100}),"root").await.unwrap();
        while result["completed"] != true {
            result = manager
                .call(
                    "wait",
                    &json!({"exec_id":result["exec_id"],"yield_time_ms":100}),
                    "poll",
                )
                .await
                .unwrap();
        }
        assert_eq!(result["isError"], true);
        assert_eq!(result["error"], "stop unconfirmed");
    }
}
