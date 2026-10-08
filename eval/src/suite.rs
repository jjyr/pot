//! Small, bounded experiments. Trial plans and delegation stay outside product Runtime.
use crate::{
    DeepSeekConnection, LiveBudget, LiveLimits, OutputAssertion, Report, Scenario,
    ScriptedResponse, ScriptedServices, records::Totals, runner::run_agent_configured,
};
use anyhow::{Result, ensure};
use futures::{FutureExt, StreamExt, future::BoxFuture};
use pot_agent::{Agent, AgentExtensions};
use pot_core::{ExecutionContext, ExecutionServices, ModelRequest, Profile, ProfileConfig};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    fs,
    io::Write,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Instant,
};

pub const MEMORY_FINAL: &str = r#"{"items":{"apple":7,"pear":4,"plum":6},"tag":"blue"}"#;
pub const TOTAL_FINAL: &str = r#"{"count":6,"total":21,"max":6}"#;
struct Config;
impl ProfileConfig for Config {
    fn model(&self) -> &str {
        "deepseek-v4-flash"
    }
    fn max_tokens(&self) -> u64 {
        128
    }
}
pub fn small_scenario(id: &str, turns: Vec<String>, tools: bool) -> Scenario {
    let mut profile = Profile::api(&Config);
    // This bounded benchmark deliberately exposes only its scenario tools.
    profile.params.agent.plugins.clear();
    profile.params.agent.system_prompt =
        Some("Complete the task. Return only the requested output.".into());
    profile.params.agent.auto_compact = false;
    profile.params.agent.request_timeout_ms = 30_000;
    profile.params.agent.builtin_tools = if tools {
        vec!["exec_command".into()]
    } else {
        vec![]
    };
    profile.params.agent.shell.default_output_bytes = 512;
    profile.params.agent.shell.max_output_bytes = 512;
    Scenario {
        id: id.into(),
        profile,
        protocol: "openai_chat".into(),
        turns,
        responses: vec![],
        fixtures: BTreeMap::new(),
        expected_files: BTreeMap::new(),
        expected_final_text: None,
        turn_assertions: vec![],
        deadline_ms: 60_000,
    }
}
pub fn memory_scenario() -> Scenario {
    let mut s = small_scenario(
        "memory-five-v1",
        vec![
            "Remember items apple=2, pear=4, and tag=red. Reply exactly ok.".into(),
            "Add item plum=6. Reply exactly ok.".into(),
            "Replace apple's value with 7. Reply exactly ok.".into(),
            "Replace tag with blue. Reply exactly ok.".into(),
            "Return only JSON with keys items (all current item values) and tag (current value)."
                .into(),
        ],
        false,
    );
    s.profile.params.max_output_tokens = 64;
    s.turn_assertions = vec![OutputAssertion::Text("ok".into()); 4];
    s.turn_assertions.push(OutputAssertion::Json(
        serde_json::from_str(MEMORY_FINAL).unwrap(),
    ));
    s
}
pub fn cache_scenarios() -> Vec<Scenario> {
    (0..5)
        .map(|i| {
            let mut s = small_scenario(
                &format!("cache-{}-v1", ["a1", "a2", "a3", "tail", "head"][i]),
                vec![if i == 3 {
                    "Reply exactly pear.".into()
                } else {
                    "Reply exactly apple.".into()
                }],
                true,
            );
            s.profile.params.max_output_tokens = 64;
            if i == 4 {
                s.profile.params.agent.system_prompt =
                    Some("Answer the task. Return only the requested output.".into());
            }
            s.turn_assertions = vec![OutputAssertion::Text(
                if i == 3 { "pear" } else { "apple" }.into(),
            )];
            s
        })
        .collect()
}
fn text_response(text: &str) -> ScriptedResponse {
    response(text, vec![])
}
fn response(text: &str, calls: Vec<Value>) -> ScriptedResponse {
    let stop = if calls.is_empty() {
        "stop"
    } else {
        "tool_calls"
    };
    ScriptedResponse {
        stage: "agent".into(),
        body_contains: vec![],
        frames: vec![
            json!({"choices":[{"delta":{"content":text,"tool_calls":calls.iter().enumerate().map(|(i,c)|json!({"index":i,"id":c["id"],"type":"function","function":{"name":c["name"],"arguments":c["arguments"].to_string()}})).collect::<Vec<_>>()},"finish_reason":null}]}),
            json!({"choices":[{"delta":{},"finish_reason":stop}]}),
            json!({"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":0}}}),
        ],
    }
}
fn call(id: &str, name: &str, arguments: Value) -> Value {
    json!({"id":id,"name":name,"arguments":arguments})
}
fn write_json(path: &Path, value: &impl serde::Serialize) -> Result<()> {
    fs::write(path, serde_json::to_vec_pretty(value)?)?;
    Ok(())
}
fn sum_reports(reports: &[Report]) -> Result<Totals> {
    let sum = |f: fn(&Totals) -> Option<u64>| -> Result<Option<u64>> {
        reports.iter().try_fold(Some(0u64), |total, r| {
            Ok(match (total, f(&r.usage)) {
                (Some(a), Some(b)) => Some(
                    a.checked_add(b)
                        .ok_or_else(|| anyhow::anyhow!("Tree usage overflow"))?,
                ),
                _ => None,
            })
        })
    };
    Ok(Totals {
        input_tokens: sum(|u| u.input_tokens)?,
        output_tokens: sum(|u| u.output_tokens)?,
        cache_read_tokens: sum(|u| u.cache_read_tokens)?,
        cache_write_tokens: sum(|u| u.cache_write_tokens)?,
        reasoning_tokens: sum(|u| u.reasoning_tokens)?,
        measurements: reports.iter().map(|r| r.usage.measurements).sum(),
    })
}

struct BudgetedServices {
    inner: Arc<dyn ExecutionServices>,
    budgets: Vec<Arc<LiveBudget>>,
}
impl ExecutionServices for BudgetedServices {
    fn model(
        &self,
        request: ModelRequest,
    ) -> BoxFuture<'_, Result<futures::stream::BoxStream<'_, Result<Vec<u8>>>>> {
        async move {
            let bound = serde_json::to_vec(&request.body)?.len() as u64
                + request.body["max_tokens"].as_u64().unwrap_or(0);
            for budget in &self.budgets {
                budget.reserve(bound)?;
            }
            self.inner.model(request).await
        }
        .boxed()
    }
    fn rpc<'a>(
        &'a self,
        session: Option<&'a str>,
        id: &'a str,
        method: &'a str,
        params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        self.inner.rpc(session, id, method, params)
    }
    fn compaction(&self, event: Value) -> BoxFuture<'_, Result<()>> {
        self.inner.compaction(event)
    }
}
#[derive(Clone)]
struct NodeRunner {
    connection: Option<DeepSeekConnection>,
    budgets: Vec<Arc<LiveBudget>>,
}
impl NodeRunner {
    fn node<'a>(
        &'a self,
        scenario: Scenario,
        directory: &'a Path,
        max_requests: u64,
        extension: Option<Arc<dyn AgentExtensions>>,
    ) -> BoxFuture<'a, Result<Report>> {
        async move {
            let (scenario, service, config, mode) = if let Some(connection) = &self.connection {
                let (s, service, config) = crate::deepseek::deepseek_services(
                    &scenario,
                    directory,
                    connection.clone(),
                    LiveLimits {
                        max_requests,
                        max_reserved_tokens: 50_000,
                    },
                    self.budgets.clone(),
                )?;
                (s, service, Some(config), "native_policy_live")
            } else {
                let service: Arc<dyn ExecutionServices> = Arc::new(BudgetedServices {
                    inner: Arc::new(ScriptedServices::new(scenario.responses.clone())),
                    budgets: self.budgets.clone(),
                });
                (scenario, service, None, "deterministic_contract")
            };
            let host = if let Some(extension) = extension {
                Agent::with_extensions(extension).services(service)
            } else {
                Agent::with_services(service)
            };
            run_agent_configured(&host, &scenario, directory, mode, config).await
        }
        .boxed()
    }
}

struct Delegation {
    runner: NodeRunner,
    directory: PathBuf,
    state: Mutex<BTreeMap<String, Option<Report>>>,
}
impl Delegation {
    fn lifecycle(&self, event: Value) -> Result<()> {
        let mut file = fs::OpenOptions::new()
            .create(true)
            .append(true)
            .open(self.directory.join("tree-events.jsonl"))?;
        serde_json::to_writer(&mut file, &event)?;
        file.write_all(b"\n")?;
        Ok(())
    }
}
impl AgentExtensions for Delegation {
    fn tools(&self, _context: &ExecutionContext) -> Result<Vec<Value>> {
        Ok(vec![
            json!({"name":"dispatch_partition","description":"Run one child Agent on the named data partition and return its verified JSON statistics. Dispatch left and right once each, then combine their results.","inputSchema":{"type":"object","properties":{"partition":{"type":"string","enum":["left","right"]}},"required":["partition"],"additionalProperties":false}}),
        ])
    }
    fn execute<'a>(
        &'a self,
        _context: &'a ExecutionContext,
        name: &'a str,
        args: &'a Value,
    ) -> BoxFuture<'a, Result<Value>> {
        async move {
            ensure!(name == "dispatch_partition", "Unknown delegation tool");
            let partition = args["partition"].as_str().ok_or_else(||anyhow::anyhow!("Partition missing"))?;
            ensure!(matches!(partition,"left"|"right"),"Invalid partition");
            {
                let mut state = self.state.lock().unwrap();
                if let Some(report) = state.get(partition) {
                    return match report { Some(report) if report.success => Ok(json!({"task_id":partition,"result":serde_json::from_str::<Value>(&report.turn_answers["turn-1"])?})), _ => Err(anyhow::anyhow!("Partition already active or failed")) };
                }
                ensure!(state.len()<2,"Child limit reached");
                state.insert(partition.into(),None);
            }
            self.lifecycle(json!({"kind":"child_started","parent":"parent","task_id":partition}))?;
            let mut s = small_scenario(&format!("child-{partition}-v1"),vec!["Use exec_command to read part.csv. Compute count, sum of amount, and maximum amount. Return only JSON with integer keys count, total, max.".into()],true);
            let (csv,expected) = if partition=="left" { ("amount\n1\n2\n3\n",json!({"count":3,"total":6,"max":3})) } else { ("amount\n4\n5\n6\n",json!({"count":3,"total":15,"max":6})) };
            s.fixtures.insert("part.csv".into(),csv.into());
            s.turn_assertions=vec![OutputAssertion::Json(expected.clone())];
            s.responses=vec![response("",vec![call("read","exec_command",json!({"cmd":"cat part.csv"}))]),text_response(&expected.to_string())];
            if self.runner.connection.is_some() { s.responses.clear(); }
            let directory = self.directory.join(partition);
            let result = self.runner.node(s,&directory,3,None).await;
            self.lifecycle(json!({"kind":"child_finished","parent":"parent","task_id":partition,"run_id":result.as_ref().ok().map(|r|r.run_id.clone()),"success":result.as_ref().is_ok_and(|r|r.success)}))?;
            let report = result?;
            self.state.lock().unwrap().insert(partition.into(),Some(report.clone()));
            ensure!(report.success && report.tool_count > 0,"Child assertion or partition-read requirement failed");
            let actual=serde_json::from_str::<Value>(&report.turn_answers["turn-1"])?;
            Ok(json!({"task_id":partition,"result":actual}))
        }.boxed()
    }
}

pub async fn run_small_suite(
    directory: &Path,
    connection: Option<DeepSeekConnection>,
) -> Result<Value> {
    fs::create_dir(directory)?;
    let started = Instant::now();
    let budget = Arc::new(LiveBudget::new(
        LiveLimits {
            max_requests: 22,
            max_reserved_tokens: 50_000,
        },
        300_000,
    )?);
    let runner = NodeRunner {
        connection,
        budgets: vec![budget.clone()],
    };
    let mode = if runner.connection.is_some() {
        "live"
    } else {
        "deterministic"
    };
    write_json(
        &directory.join("suite-manifest.json"),
        &json!({"schema_version":1,"suite":"small-budget-v1","mode":mode,"trials":1,"limits":budget.snapshot(),"model":"deepseek-v4-flash","thinking":"off","environment":"local fixture; no VM sandbox","faults":"deterministic only","tree_policy":"directed, serial, depth=1, children=2","system_prompt":"Complete the task. Return only the requested output."}),
    )?;
    let mut memory = memory_scenario();
    memory.responses = vec![text_response("ok"); 4];
    memory.responses.push(text_response(MEMORY_FINAL));
    if runner.connection.is_some() {
        memory.responses.clear();
    }
    let memory = runner
        .node(memory, &directory.join("memory"), 5, None)
        .await?;
    write_json(&directory.join("memory-result.json"), &memory)?;
    let mut caches = vec![];
    for (i, mut s) in cache_scenarios().into_iter().enumerate() {
        if runner.connection.is_none() {
            s.responses = vec![text_response(if i == 3 { "pear" } else { "apple" })];
        }
        if i > 0 {
            tokio::time::sleep(std::time::Duration::from_millis(200)).await;
        }
        let report = runner
            .node(s, &directory.join(format!("cache-{i}")), 1, None)
            .await?;
        caches.push(report);
    }
    let cache_identity = if runner.connection.is_some() {
        let bodies = (0..5)
            .map(|i| -> Result<Value> {
                let text = fs::read_to_string(directory.join(format!("cache-{i}/requests.jsonl")))?;
                Ok(serde_json::from_str::<Value>(
                    text.lines()
                        .next()
                        .ok_or_else(|| anyhow::anyhow!("Missing cache request"))?,
                )?["body"]
                    .clone())
            })
            .collect::<Result<Vec<_>>>()?;
        ensure!(
            bodies[0] == bodies[1] && bodies[1] == bodies[2],
            "Exact-repeat cache requests differ"
        );
        ensure!(
            bodies[0]["messages"][0] == bodies[3]["messages"][0]
                && bodies[0]["tools"] == bodies[3]["tools"],
            "Tail mutation changed prefix"
        );
        ensure!(
            bodies[0]["messages"][1] == bodies[4]["messages"][1]
                && bodies[0]["tools"] == bodies[4]["tools"]
                && bodies[0]["messages"][0] != bodies[4]["messages"][0],
            "Head mutation changed task/tools"
        );
        "verified from captured bodies"
    } else {
        "synthetic trace; no empirical cache claim"
    };
    write_json(
        &directory.join("cache-result.json"),
        &json!({"runs":caches,"identity":cache_identity,"delay_ms":200,"cache_condition":"uncontrolled; first request is not guaranteed cold","effect_observed":caches.iter().skip(1).any(|r|r.usage.cache_read_tokens.is_some_and(|n|n>0))}),
    )?;
    let mut single=small_scenario("single-worker-v1",vec!["Use exec_command to read left.csv and right.csv. Compute the combined count, sum of amount, and maximum amount. Return only JSON with integer keys count, total, max.".into()],true);
    single.fixtures = BTreeMap::from([
        ("left.csv".into(), "amount\n1\n2\n3\n".into()),
        ("right.csv".into(), "amount\n4\n5\n6\n".into()),
    ]);
    single.turn_assertions = vec![OutputAssertion::Json(serde_json::from_str(TOTAL_FINAL)?)];
    if runner.connection.is_none() {
        single.responses = vec![
            response(
                "",
                vec![call(
                    "read",
                    "exec_command",
                    json!({"cmd":"cat left.csv right.csv"}),
                )],
            ),
            text_response(TOTAL_FINAL),
        ];
    }
    let single = runner
        .node(single, &directory.join("single-worker"), 4, None)
        .await?;
    let tree = run_tree(&runner, &directory.join("split")).await?;
    let tree_complete = tree.tree_complete;
    let tree_usage = tree.tree_usage.clone();
    let parent = tree.parent.clone();
    let split_success = tree.success();
    let mut tree_result = serde_json::to_value(&tree)?;
    tree_result["single"] = serde_json::to_value(&single)?;
    tree_result["workload"] = json!("six-row-statistics-v1");
    tree_result["comparison_mode"] = json!("native task architectures; different tool sets");
    write_json(&directory.join("tree-result.json"), &tree_result)?;
    let faults = run_fault_suite(&directory.join("faults")).await?;
    let success = memory.success
        && memory.request_count == 5
        && memory.tool_count == 0
        && caches
            .iter()
            .all(|r| r.success && r.request_count == 1 && r.tool_count == 0)
        && single.success
        && tree_complete
        && split_success
        && faults["success"] == true;
    let summary = json!({"schema_version":1,"suite":"small-budget-v1","mode":mode,"success":success,"elapsed_ms":started.elapsed().as_millis(),"memory":{"usage":memory.usage,"turn_usage":memory.turn_usage,"success":memory.success},"cache":caches.iter().map(|r|json!({"case":r.scenario_id,"usage":r.usage,"success":r.success})).collect::<Vec<_>>(),"delegation":{"single_usage":single.usage,"tree_usage":tree_usage,"parent_usage":parent.usage,"tree_complete":tree_complete,"single_success":single.success,"split_success":split_success},"faults":faults,"budget":budget.snapshot(),"empirical_efficiency_claim":false});
    write_json(&directory.join("summary.json"), &summary)?;
    Ok(summary)
}

#[derive(serde::Serialize)]
struct TreeReport {
    parent: Report,
    children: BTreeMap<String, Option<Report>>,
    tree_complete: bool,
    tree_usage: Totals,
    tree_limits: Value,
    elapsed_ms: u128,
}
impl TreeReport {
    fn success(&self) -> bool {
        self.tree_complete
            && self.parent.success
            && self.parent.tool_count == 2
            && self
                .children
                .values()
                .all(|r| r.as_ref().is_some_and(|r| r.success && r.tool_count > 0))
    }
}
async fn run_tree(runner: &NodeRunner, directory: &Path) -> Result<TreeReport> {
    let started = Instant::now();
    fs::create_dir(directory)?;
    let tree_budget = Arc::new(LiveBudget::new(
        LiveLimits {
            max_requests: 8,
            max_reserved_tokens: 30_000,
        },
        120_000,
    )?);
    let mut tree_runner = runner.clone();
    tree_runner.budgets.insert(0, tree_budget.clone());
    let delegation = Arc::new(Delegation {
        runner: tree_runner.clone(),
        directory: directory.to_path_buf(),
        state: Mutex::new(BTreeMap::new()),
    });
    let mut parent=small_scenario("split-parent-v1",vec!["Dispatch left and right using dispatch_partition exactly once each. Combine the two child results. Return only JSON with integer keys count, total, max. Do not compute results without dispatching both tasks.".into()],false);
    parent.turn_assertions = vec![OutputAssertion::Json(serde_json::from_str(TOTAL_FINAL)?)];
    if runner.connection.is_none() {
        parent.responses = vec![
            response(
                "",
                vec![
                    call("left", "dispatch_partition", json!({"partition":"left"})),
                    call("right", "dispatch_partition", json!({"partition":"right"})),
                ],
            ),
            text_response(TOTAL_FINAL),
        ];
    }
    if !parent.responses.is_empty() {
        parent.responses[0].body_contains = vec![
            "\"partition\"".into(),
            "\"enum\":[\"left\",\"right\"]".into(),
        ];
    }
    let parent = tree_runner
        .node(
            parent,
            &directory.join("parent"),
            4,
            Some(delegation.clone()),
        )
        .await?;
    let children = delegation.state.lock().unwrap().clone();
    let child_reports = children
        .values()
        .filter_map(Clone::clone)
        .collect::<Vec<_>>();
    let tree_complete = children.len() == 2 && child_reports.len() == 2;
    let mut tree_reports = vec![parent.clone()];
    tree_reports.extend(child_reports.clone());
    let tree_usage = if tree_complete {
        sum_reports(&tree_reports)?
    } else {
        Totals::default()
    };
    Ok(TreeReport {
        parent,
        children,
        tree_complete,
        tree_usage,
        tree_limits: tree_budget.snapshot(),
        elapsed_ms: started.elapsed().as_millis(),
    })
}
/// Run only the delegation case, retaining a separate bounded record.
pub async fn run_delegation_suite(
    directory: &Path,
    connection: Option<DeepSeekConnection>,
) -> Result<Value> {
    let runner = NodeRunner {
        connection,
        budgets: vec![],
    };
    let tree = run_tree(&runner, directory).await?;
    let mut result = serde_json::to_value(&tree)?;
    result["success"] = json!(tree.success());
    result["workload"] = json!("six-row-statistics-v1");
    write_json(&directory.join("tree-result.json"), &result)?;
    Ok(result)
}

#[derive(Clone, Copy)]
enum Fault {
    None,
    Http(u16),
    Connection,
    Interrupted,
}
struct FaultServices {
    inner: ScriptedServices,
    fault: Fault,
    failures: usize,
    retry: bool,
    attempts: Mutex<Vec<Value>>,
}
impl ExecutionServices for FaultServices {
    fn model(
        &self,
        request: ModelRequest,
    ) -> BoxFuture<'_, Result<futures::stream::BoxStream<'_, Result<Vec<u8>>>>> {
        async move {
            let limit=if self.retry {2} else {1};
            for attempt in 1..=limit {
                let failure=attempt<=self.failures;
                self.attempts.lock().unwrap().push(json!({"logical_request":request.id,"attempt_id":format!("{}/attempt-{attempt}",request.id),"attempt":attempt,"failure":failure,"http_status":if failure {match self.fault {Fault::Http(status)=>Some(status),_=>None}} else {None},"retry_delay_ms":if failure && attempt<limit && matches!(self.fault,Fault::Http(429|503)|Fault::Connection) {1} else {0},"retry_policy":if self.retry {"eval transport: max 2 attempts"} else {"production default: disabled"}}));
                if failure {
                    match self.fault {
                        Fault::Http(429|503)|Fault::Connection if attempt<limit => {tokio::time::sleep(std::time::Duration::from_millis(1)).await;continue;},
                        Fault::Interrupted => {
                            let partial=json!({"choices":[{"delta":{"tool_calls":[{"index":0,"id":"partial","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"touch should-not-exist"}}]},"finish_reason":null}]});
                            return Ok(futures::stream::iter(vec![Ok(format!("data: {partial}\n\n").into_bytes()),Err(anyhow::anyhow!("Injected mid-stream failure; usage unknown"))]).boxed());
                        },
                        _ => anyhow::bail!("Injected failure; no provider usage observed"),
                    }
                }
                return self.inner.model(request).await;
            }
            anyhow::bail!("Attempt limit exhausted")
        }.boxed()
    }
    fn rpc<'a>(
        &'a self,
        session: Option<&'a str>,
        id: &'a str,
        method: &'a str,
        params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        self.inner.rpc(session, id, method, params)
    }
    fn compaction(&self, event: Value) -> BoxFuture<'_, Result<()>> {
        self.inner.compaction(event)
    }
}
pub async fn run_fault_suite(directory: &Path) -> Result<Value> {
    fs::create_dir(directory)?;
    let mut cases = vec![];
    for (name, fault, failures, retry, expected_success, expected_attempts) in [
        ("control", Fault::None, 0, false, true, 1),
        ("retry-disabled", Fault::Http(429), 1, false, false, 1),
        ("429-retry", Fault::Http(429), 1, true, true, 2),
        ("503-retry", Fault::Http(503), 1, true, true, 2),
        ("connection-retry", Fault::Connection, 1, true, true, 2),
        ("auth-no-retry", Fault::Http(401), 1, true, false, 1),
        ("invalid-no-retry", Fault::Http(400), 1, true, false, 1),
        ("retry-exhausted", Fault::Http(503), 2, true, false, 2),
        ("stream-interrupted", Fault::Interrupted, 1, true, false, 1),
    ] {
        let mut s = small_scenario(name, vec!["Reply exactly done.".into()], true);
        s.expected_final_text = Some("done".into());
        let service = Arc::new(FaultServices {
            inner: ScriptedServices::new(vec![text_response("done")]),
            fault,
            failures,
            retry,
            attempts: Mutex::new(vec![]),
        });
        let host = Agent::with_services(service.clone());
        let path = directory.join(name);
        let report = crate::run_agent(&host, &s, &path).await?;
        let attempts = service.attempts.lock().unwrap().clone();
        write_json(&path.join("attempts.json"), &attempts)?;
        let passed = report.success == expected_success
            && attempts.len() == expected_attempts
            && report.tool_count == 0
            && !path.join("workspace/should-not-exist").exists()
            && (expected_success
                || (!report.measurement_complete && report.usage.input_tokens.is_none()));
        cases.push(json!({"case":name,"pass":passed,"expected_success":expected_success,"actual_success":report.success,"attempts":attempts.len(),"usage":report.usage,"measurement_complete":report.measurement_complete}));
    }
    for duplicate in [false, true] {
        let name = if duplicate {
            "duplicate-effect"
        } else {
            "tool-recovery"
        };
        let mut s = small_scenario(name, vec!["Complete and reply done.".into()], true);
        s.expected_final_text = Some("done".into());
        let tool = call(
            "effect",
            "exec_command",
            json!({"cmd":"printf 'once\\n' >> effect.txt"}),
        );
        s.responses = if duplicate {
            s.expected_files
                .insert("effect.txt".into(), "once\n".into());
            vec![
                response("", vec![tool.clone()]),
                response("", vec![tool]),
                text_response("done"),
            ]
        } else {
            vec![
                response(
                    "",
                    vec![call("fail", "exec_command", json!({"cmd":"exit 1"}))],
                ),
                response(
                    "",
                    vec![call("correct", "exec_command", json!({"cmd":"true"}))],
                ),
                text_response("done"),
            ]
        };
        let report = crate::run_scripted(&s, &directory.join(name)).await?;
        cases.push(json!({"case":name,"pass":report.success && report.tool_count==2,"actual_success":report.success,"usage":report.usage,"attempts":report.request_count}));
    }
    let summary = json!({"mode":"deterministic_contract","success":cases.iter().all(|c|c["pass"]==true),"cases":cases,"empirical_efficiency_claim":false,"retry_scope":"Explicit eval transport policy; production automatic retry remains disabled"});
    write_json(&directory.join("summary.json"), &summary)?;
    Ok(summary)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[tokio::test]
    async fn bounded_suite_exercises_memory_cache_delegation_and_failures() {
        let path = std::env::temp_dir().join(format!("pot-small-suite-{}", uuid::Uuid::new_v4()));
        let report = run_small_suite(&path, None).await.unwrap();
        assert_eq!(
            report["success"],
            true,
            "{}",
            serde_json::to_string_pretty(&report).unwrap()
        );
        assert_eq!(report["delegation"]["tree_usage"]["input_tokens"], 60);
        assert_eq!(report["delegation"]["single_usage"]["input_tokens"], 20);
        assert_eq!(report["budget"]["attempts"], 18);
        assert_eq!(
            crate::validate(&path.join("memory"))
                .unwrap()
                .turn_usage
                .len(),
            5
        );
        fs::remove_dir_all(path).unwrap();
    }
    #[test]
    fn tree_budget_rejects_extra_attempts_and_oversized_payloads() {
        let budget = LiveBudget::new(
            LiveLimits {
                max_requests: 2,
                max_reserved_tokens: 10,
            },
            1000,
        )
        .unwrap();
        budget.reserve(4).unwrap();
        assert!(budget.reserve(7).is_err());
        budget.reserve(6).unwrap();
        assert!(budget.reserve(0).is_err());
        assert_eq!(budget.snapshot()["attempts"], 2);
    }
    #[tokio::test]
    async fn intermediate_failure_is_not_hidden_by_a_correct_final_answer() {
        let path =
            std::env::temp_dir().join(format!("pot-turn-assertion-{}", uuid::Uuid::new_v4()));
        let mut s = small_scenario("turn-check", vec!["first".into(), "last".into()], false);
        s.turn_assertions = vec![
            OutputAssertion::Text("ok".into()),
            OutputAssertion::Json(json!({"done":true})),
        ];
        s.responses = vec![text_response("wrong"), text_response("{\"done\":true}")];
        let report = crate::run_scripted(&s, &path).await.unwrap();
        assert!(!report.success);
        assert!(report.errors.iter().any(|e| e.contains("turn-1")));
        assert_eq!(report.turn_usage["turn-1"].input_tokens, Some(10));
        assert_eq!(report.turn_usage["turn-2"].input_tokens, Some(10));
        fs::remove_dir_all(path).unwrap();
    }
    #[tokio::test]
    async fn duplicate_dispatch_and_swapped_completion_order_do_not_duplicate_children() {
        let path = std::env::temp_dir().join(format!("pot-child-order-{}", uuid::Uuid::new_v4()));
        fs::create_dir(&path).unwrap();
        let service = Delegation {
            runner: NodeRunner {
                connection: None,
                budgets: vec![],
            },
            directory: path.clone(),
            state: Mutex::new(BTreeMap::new()),
        };
        let context = ExecutionContext {
            instructions: String::new(),
            skill_resources: Default::default(),
            tool_executor: Some(Arc::new(pot_tool_executor::LocalTools::default())),
            data: path.clone(),
            workspace: path.clone(),
            owner: "eval".into(),
            role: pot_core::ExecutionRole::Worker,
            backend_url: String::new(),
            lane_token: String::new(),
            main_prompt: String::new(),
            feedback: Arc::new(
                crate::runner::RunFeedback::new(&path.join("unused-events.jsonl"), "test".into())
                    .unwrap(),
            ),
        };
        let right = service
            .execute(
                &context,
                "dispatch_partition",
                &json!({"partition":"right"}),
            )
            .await
            .unwrap();
        let again = service
            .execute(
                &context,
                "dispatch_partition",
                &json!({"partition":"right"}),
            )
            .await
            .unwrap();
        assert_eq!(right, again);
        service
            .execute(&context, "dispatch_partition", &json!({"partition":"left"}))
            .await
            .unwrap();
        assert_eq!(service.state.lock().unwrap().len(), 2);
        assert_eq!(
            fs::read_to_string(path.join("tree-events.jsonl"))
                .unwrap()
                .lines()
                .count(),
            4
        );
        fs::remove_dir_all(path).unwrap();
    }
    #[tokio::test]
    async fn exhausted_child_budget_keeps_failure_and_does_not_spawn_a_replacement() {
        let path = std::env::temp_dir().join(format!("pot-child-fail-{}", uuid::Uuid::new_v4()));
        fs::create_dir(&path).unwrap();
        let budget = Arc::new(
            LiveBudget::new(
                LiveLimits {
                    max_requests: 1,
                    max_reserved_tokens: 10_000,
                },
                5000,
            )
            .unwrap(),
        );
        let service = Delegation {
            runner: NodeRunner {
                connection: None,
                budgets: vec![budget.clone()],
            },
            directory: path.clone(),
            state: Mutex::new(BTreeMap::new()),
        };
        let context = ExecutionContext {
            instructions: String::new(),
            skill_resources: Default::default(),
            tool_executor: Some(Arc::new(pot_tool_executor::LocalTools::default())),
            data: path.clone(),
            workspace: path.clone(),
            owner: "eval".into(),
            role: pot_core::ExecutionRole::Worker,
            backend_url: String::new(),
            lane_token: String::new(),
            main_prompt: String::new(),
            feedback: Arc::new(
                crate::runner::RunFeedback::new(&path.join("unused-events.jsonl"), "test".into())
                    .unwrap(),
            ),
        };
        assert!(
            service
                .execute(&context, "dispatch_partition", &json!({"partition":"left"}))
                .await
                .is_err()
        );
        assert!(
            service
                .execute(&context, "dispatch_partition", &json!({"partition":"left"}))
                .await
                .is_err()
        );
        let report = crate::validate(&path.join("left")).unwrap();
        assert!(!report.success && !report.measurement_complete);
        assert_eq!(budget.snapshot()["attempts"], 1);
        assert_eq!(service.state.lock().unwrap().len(), 1);
        fs::remove_dir_all(path).unwrap();
    }
}
