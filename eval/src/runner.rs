use crate::records::*;
use anyhow::{Result, ensure};
use futures::{FutureExt, StreamExt, future::BoxFuture};
use pot_core::*;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use std::{
    collections::{BTreeMap, VecDeque},
    fs::{self, File, OpenOptions},
    io::{BufRead, BufReader, Write},
    path::{Component, Path},
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::Instant,
};
use tokio::sync::Notify;

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Scenario {
    pub id: String,
    pub profile: Profile,
    pub protocol: String,
    pub turns: Vec<String>,
    pub responses: Vec<ScriptedResponse>,
    #[serde(default)]
    pub fixtures: BTreeMap<String, String>,
    #[serde(default)]
    pub expected_files: BTreeMap<String, String>,
    #[serde(default)]
    pub expected_final_text: Option<String>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub turn_assertions: Vec<OutputAssertion>,
    pub deadline_ms: u64,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(tag = "kind", content = "expected", rename_all = "snake_case")]
pub enum OutputAssertion {
    Text(String),
    Json(Value),
}
impl OutputAssertion {
    pub fn accepts(&self, text: &str) -> bool {
        match self {
            Self::Text(expected) => text == expected,
            Self::Json(expected) => {
                serde_json::from_str::<Value>(text).ok().as_ref() == Some(expected)
            }
        }
    }
}
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ScriptedResponse {
    /// Agent or compaction: enforces the expected request ordering.
    pub stage: String,
    /// Native provider SSE JSON frames; this exercises the actual gateway decoder.
    pub frames: Vec<Value>,
    #[serde(default)]
    pub body_contains: Vec<String>,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Report {
    pub schema_version: u32,
    pub run_id: String,
    pub scenario_id: String,
    pub scenario_hash: String,
    pub mode: String,
    pub success: bool,
    pub errors: Vec<String>,
    pub usage: Totals,
    pub stage_usage: BTreeMap<String, Totals>,
    #[serde(default)]
    pub turn_usage: BTreeMap<String, Totals>,
    #[serde(default)]
    pub turn_answers: BTreeMap<String, String>,
    pub request_count: usize,
    pub tool_count: usize,
    pub compaction_count: usize,
    pub measurement_complete: bool,
    #[serde(default)]
    pub transport_hash: Option<String>,
}
fn hash(value: &impl Serialize) -> Result<String> {
    Ok(Sha256::digest(serde_json::to_vec(value)?)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect())
}
fn relative(root: &Path, name: &str) -> Result<std::path::PathBuf> {
    let path = Path::new(name);
    ensure!(
        !name.is_empty() && path.components().all(|c| matches!(c, Component::Normal(_))),
        "Fixture paths must be relative normal components"
    );
    Ok(root.join(path))
}
fn write_json(path: &Path, value: &impl Serialize) -> Result<()> {
    fs::write(path, serde_json::to_vec_pretty(value)?)?;
    Ok(())
}
pub struct ScriptedServices {
    responses: Mutex<VecDeque<ScriptedResponse>>,
    requests: Mutex<Vec<Value>>,
}
impl ScriptedServices {
    pub fn new(responses: Vec<ScriptedResponse>) -> Self {
        Self {
            responses: Mutex::new(responses.into()),
            requests: Mutex::new(vec![]),
        }
    }
    pub fn remaining(&self) -> usize {
        self.responses.lock().unwrap().len()
    }
}
impl ExecutionServices for ScriptedServices {
    fn model(
        &self,
        request: ModelRequest,
    ) -> BoxFuture<'_, Result<futures::stream::BoxStream<'_, Result<Vec<u8>>>>> {
        async move {
            self.requests.lock().unwrap().push(json!({"id":request.id,"protocol":request.protocol,"model":request.model,"compaction_id":request.compaction_id,"body":request.body}));
            let response = self.responses.lock().unwrap().pop_front().ok_or_else(|| anyhow::anyhow!("Scripted model exhausted"))?;
            let stage = if request.compaction_id.is_some() {"compaction"} else {"agent"};
            ensure!(response.stage == stage, "Expected {} request, received {stage}",response.stage);
            let body = request.body.to_string();
            for expected in response.body_contains { ensure!(body.contains(&expected), "Expected request fragment missing: {expected}"); }
            let bytes = response.frames.into_iter().map(|frame| Ok(format!("data: {frame}\n\n").into_bytes())).collect::<Vec<_>>();
            Ok(futures::stream::iter(bytes).boxed())
        }.boxed()
    }
    fn rpc<'a>(
        &'a self,
        _session: Option<&'a str>,
        _id: &'a str,
        _method: &'a str,
        _params: Value,
    ) -> BoxFuture<'a, Result<(Value, Option<String>)>> {
        async { anyhow::bail!("External tools are not configured for this scripted run") }.boxed()
    }
    fn compaction(&self, _event: Value) -> BoxFuture<'_, Result<()>> {
        async { Ok(()) }.boxed()
    }
}
/// Run-local feedback; does not settle product allowances or touch product databases.
pub struct RunFeedback {
    file: Mutex<File>,
    state: Mutex<(u64, String, Option<Value>)>,
    run_id: String,
    started: Instant,
    cancelled: AtomicBool,
    wake: Notify,
}
impl RunFeedback {
    pub(crate) fn new(path: &Path, run_id: String) -> Result<Self> {
        Ok(Self {
            file: Mutex::new(OpenOptions::new().write(true).create_new(true).open(path)?),
            state: Mutex::new((0, String::new(), None)),
            run_id,
            started: Instant::now(),
            cancelled: AtomicBool::new(false),
            wake: Notify::new(),
        })
    }
    fn record(&self, payload: Payload) -> Result<()> {
        let mut state = self.state.lock().unwrap();
        state.0 += 1;
        let event = Event {
            schema_version: SCHEMA,
            run_id: self.run_id.clone(),
            seq: state.0,
            elapsed_ms: self.started.elapsed().as_millis() as u64,
            turn_id: state.1.clone(),
            payload,
        };
        let mut file = self.file.lock().unwrap();
        serde_json::to_writer(&mut *file, &event)?;
        file.write_all(b"\n")?;
        file.flush()?;
        Ok(())
    }
    fn cancel(&self) {
        self.cancelled.store(true, Ordering::SeqCst);
        self.wake.notify_one();
    }
}
impl ExecutionFeedback for RunFeedback {
    fn observe(&self, event: &ExecutionObservation) -> Result<()> {
        self.record(Payload::Observation(event.clone()))
    }
    fn load_context(&self) -> Result<Option<Value>> {
        Ok(self.state.lock().unwrap().2.clone())
    }
    fn save_context(&self, value: &Value) -> Result<()> {
        self.state.lock().unwrap().2 = Some(value.clone());
        Ok(())
    }
    fn cancelled(&self) -> bool {
        self.cancelled.load(Ordering::SeqCst)
    }
    fn cancellation(&self) -> BoxFuture<'_, ()> {
        async {
            if !self.cancelled() {
                self.wake.notified().await;
            }
        }
        .boxed()
    }
    fn native_identity(&self, _identity: &str) -> Result<()> {
        Ok(())
    }
    fn binding_broken(&self, reason: &str) -> Result<()> {
        anyhow::bail!("Binding broken: {reason}")
    }
    fn needs_rebind(&self, reason: &str) -> Result<()> {
        anyhow::bail!("Binding needs rebind: {reason}")
    }
    fn record_turn(&self, request: &str) -> Result<()> {
        self.state.lock().unwrap().1 = request.into();
        Ok(())
    }
    fn input_streaming(&self, _request: &str) -> Result<()> {
        Ok(())
    }
}
/// Generic Driver runner. Caller chooses a host; no second Agent adapter contract.
pub async fn run_driver(
    host: &dyn DriverHost,
    scenario: &Scenario,
    directory: &Path,
) -> Result<Report> {
    run_driver_configured(host, scenario, directory, "deterministic_contract", None).await
}
pub(crate) async fn run_driver_configured(
    host: &dyn DriverHost,
    scenario: &Scenario,
    directory: &Path,
    mode: &str,
    transport: Option<Value>,
) -> Result<Report> {
    ensure!(
        matches!(mode, "deterministic_contract" | "native_policy_live"),
        "Unsupported evaluation mode"
    );
    ensure!(
        !scenario.id.is_empty() && !scenario.turns.is_empty(),
        "Scenario identity/turns missing"
    );
    ensure!(
        scenario.turn_assertions.is_empty()
            || scenario.turn_assertions.len() == scenario.turns.len(),
        "Turn assertion count mismatch"
    );
    ensure!(
        (1..=3_600_000).contains(&scenario.deadline_ms),
        "Deadline must be 1ms..1h"
    );
    let mut spec = scenario.profile.resolve()?;
    spec.set_gateway_protocol(&scenario.protocol)?;
    let capabilities = host.describe(&spec)?;
    ensure!(
        capabilities.injectable_services,
        "This delivery requires a Driver with injectable standalone services"
    );
    fs::create_dir(directory)?; // Never overwrite a prior run.
    let workspace = directory.join("workspace");
    fs::create_dir(&workspace)?;
    for (name, content) in &scenario.fixtures {
        let path = relative(&workspace, name)?;
        fs::create_dir_all(path.parent().unwrap())?;
        fs::write(path, content)?;
    }
    let run_id = uuid::Uuid::new_v4().to_string();
    let manifest = Manifest {
        schema_version: SCHEMA,
        run_id: run_id.clone(),
        scenario_id: scenario.id.clone(),
        scenario_hash: hash(scenario)?,
        mode: mode.into(),
        driver: format!("{:?}", spec.engine),
        build_version: env!("CARGO_PKG_VERSION").into(),
        runtime_revision: option_env!("POT_EVAL_REVISION").map(str::to_owned),
        effective_spec: spec.clone(),
        capabilities,
        deadline_ms: scenario.deadline_ms,
        transport,
    };
    write_json(&directory.join("manifest.json"), &manifest)?;
    write_json(&directory.join("scenario.json"), scenario)?;
    let feedback = Arc::new(RunFeedback::new(&directory.join("events.jsonl"), run_id)?);
    let binding = Binding {
        pot_owner: "eval".into(),
        epoch: 1,
        spec,
        native_ref: NativeRef::Pot {
            file: directory.join("context.json"),
            session_id: None,
        },
        schema_version: SCHEMA_VERSION,
        state: BindingState::Active,
        model_switches: vec![],
        handoff_ref: None,
        failure: None,
    };
    let context = ExecutionContext {
        instructions: String::new(),
        skill_resources: Default::default(),
        tool_executor: Some(Arc::new(pot_tool_executor::LocalTools::default())),
        data: directory.to_path_buf(),
        workspace,
        owner: "eval".into(),
        role: ExecutionRole::Worker,
        backend_url: String::new(),
        lane_token: String::new(),
        main_prompt: String::new(),
        feedback: feedback.clone(),
    };
    let mut session = host.session(context, binding)?;
    let started = Instant::now();
    let mut execution_errors = vec![];
    for (i, text) in scenario.turns.iter().enumerate() {
        let request = TurnRequest {
            request_id: format!("turn-{}", i + 1),
            text: text.clone(),
        };
        feedback.record_turn(&request.request_id)?;
        let mut sink = |event| feedback.record(Payload::Driver(event));
        let turn = session.start_turn(&request, false, &mut sink);
        tokio::pin!(turn);
        let remaining = std::time::Duration::from_millis(scenario.deadline_ms)
            .saturating_sub(started.elapsed());
        let result = tokio::select! {
            result = &mut turn => result,
            _ = tokio::time::sleep(remaining) => {
                feedback.cancel();
                let cleanup = tokio::time::timeout(std::time::Duration::from_secs(10),&mut turn).await;
                execution_errors.push("Run deadline exceeded".into());
                match cleanup { Ok(result) => result, Err(_) => Err(anyhow::anyhow!("Cancellation cleanup not confirmed")) }
            }
        };
        if let Err(error) = result {
            execution_errors.push(error.to_string());
        }
        if feedback.cancelled() || !execution_errors.is_empty() {
            break;
        }
    }
    if let Err(error) = session.close().await {
        execution_errors.push(error.to_string());
    }
    write_json(&directory.join("execution.json"), &execution_errors)?;
    if let Some(context) = feedback.load_context()? {
        write_json(&directory.join("context.json"), &context)?;
    }
    let report = validate(directory)?;
    write_json(&directory.join("report.json"), &report)?;
    Ok(report)
}

pub async fn run_scripted(scenario: &Scenario, directory: &Path) -> Result<Report> {
    ensure!(
        scenario.profile.engine == Engine::Pot,
        "Scripted transport requires Pot Agent"
    );
    let services = Arc::new(ScriptedServices::new(scenario.responses.clone()));
    let host = pot_agent::AgentHost::with_services(services.clone());
    let mut report = run_driver(&host, scenario, directory).await?;
    write_json(
        &directory.join("requests.json"),
        &*services.requests.lock().unwrap(),
    )?;
    if services.remaining() != 0 {
        let mut errors: Vec<String> =
            serde_json::from_slice(&fs::read(directory.join("execution.json"))?)?;
        errors.push(format!(
            "{} scripted responses unused",
            services.remaining()
        ));
        write_json(&directory.join("execution.json"), &errors)?;
        report = validate(directory)?;
        write_json(&directory.join("report.json"), &report)?;
    }
    Ok(report)
}

pub fn validate(directory: &Path) -> Result<Report> {
    let manifest: Manifest = serde_json::from_slice(&fs::read(directory.join("manifest.json"))?)?;
    let scenario: Scenario = serde_json::from_slice(&fs::read(directory.join("scenario.json"))?)?;
    ensure!(
        manifest.schema_version == SCHEMA && manifest.scenario_hash == hash(&scenario)?,
        "Manifest/scenario mismatch"
    );
    manifest.effective_spec.validate()?;
    let mut expected_spec = scenario.profile.resolve()?;
    expected_spec.set_gateway_protocol(&scenario.protocol)?;
    ensure!(
        manifest.effective_spec == expected_spec
            && manifest.scenario_id == scenario.id
            && matches!(
                manifest.mode.as_str(),
                "deterministic_contract" | "native_policy_live"
            ),
        "Effective configuration differs from scenario"
    );
    let mut errors: Vec<String> =
        serde_json::from_slice(&fs::read(directory.join("execution.json"))?)?;
    let mut usage_seen = std::collections::BTreeSet::new();
    let mut requests: BTreeMap<String, (String, bool, String)> = BTreeMap::new();
    let mut compactions = BTreeMap::new();
    let mut gates = BTreeMap::new();
    let mut reports = vec![];
    let mut active_tools = BTreeMap::new();
    let mut tool_count = 0;
    let mut terminals = 0;
    let mut final_text = String::new();
    let mut turn_answers = BTreeMap::new();
    let mut previous_time = 0;
    for (index, line) in BufReader::new(File::open(directory.join("events.jsonl"))?)
        .lines()
        .enumerate()
    {
        let event: Event = serde_json::from_str(&line?)?;
        ensure!(
            event.schema_version == SCHEMA
                && event.run_id == manifest.run_id
                && event.seq == index as u64 + 1,
            "Invalid event identity/order"
        );
        ensure!(
            event.elapsed_ms >= previous_time,
            "Event time moved backwards"
        );
        previous_time = event.elapsed_ms;
        ensure!(
            scenario
                .turns
                .iter()
                .enumerate()
                .any(|(i, _)| event.turn_id == format!("turn-{}", i + 1)),
            "Unknown turn"
        );
        match event.payload {
            Payload::Driver(driver) => {
                ensure!(driver.turn_id == event.turn_id, "Driver turn mismatch");
                let gate = gates
                    .entry(event.turn_id.clone())
                    .or_insert_with(|| EventGate::new(1, &driver.turn_id));
                ensure!(gate.accept(&driver), "Invalid Driver event order/terminal");
                match driver.update {
                    Update::ToolStarted { id, name, .. } => {
                        ensure!(
                            active_tools.insert((driver.turn_id, id), name).is_none(),
                            "Tool started twice while active"
                        );
                        tool_count += 1;
                    }
                    Update::ToolOutput { id, .. } => {
                        ensure!(
                            active_tools.contains_key(&(driver.turn_id, id)),
                            "Output without active tool"
                        );
                    }
                    Update::ToolFinished { id, name, .. } => {
                        ensure!(
                            active_tools.remove(&(driver.turn_id, id)).as_ref() == Some(&name),
                            "Tool finish without matching start"
                        );
                    }
                    Update::MessageFinished { text, .. } => {
                        turn_answers.insert(event.turn_id.clone(), text.clone());
                        final_text = text;
                    }
                    Update::TurnFinished(outcome) => {
                        terminals += 1;
                        if outcome != Outcome::Success {
                            errors.push(format!("Turn outcome: {outcome:?}"));
                        }
                    }
                    _ => {}
                }
            }
            Payload::Observation(ExecutionObservation::RequestStarted {
                id,
                stage,
                compaction_id,
            }) => {
                ensure!(
                    matches!(stage.as_str(), "agent" | "compaction"),
                    "Unknown request stage"
                );
                if stage == "compaction" {
                    ensure!(
                        compaction_id
                            .as_ref()
                            .is_some_and(|id| compactions.get(id) == Some(&false)),
                        "Compaction request without active lifecycle"
                    );
                } else {
                    ensure!(
                        compaction_id.is_none(),
                        "Agent request linked to compaction"
                    );
                }
                ensure!(
                    requests.insert(id, (stage, false, event.turn_id)).is_none(),
                    "Duplicate request start"
                );
            }
            Payload::Observation(ExecutionObservation::UsageObserved {
                id,
                input_tokens,
                output_tokens,
                cache_read_tokens,
                cache_write_tokens,
                reasoning_tokens,
            }) => {
                ensure!(
                    requests
                        .get(&id)
                        .is_some_and(|r| !r.1 && r.2 == event.turn_id),
                    "Usage outside active request"
                );
                usage_seen.insert(id.clone());
                reports.push(UsageReport {
                    measurement_id: id,
                    source_event_id: format!("usage-{}", event.seq),
                    scope: Scope::Request,
                    source: "provider".into(),
                    counter: Counter::Snapshot,
                    complete: input_tokens.is_some() && output_tokens.is_some(),
                    input_tokens,
                    output_tokens,
                    cache_read_tokens,
                    cache_write_tokens,
                    reasoning_tokens,
                    baseline_input: None,
                    baseline_output: None,
                });
            }
            Payload::Observation(ExecutionObservation::RequestFinished {
                id,
                input_tokens,
                output_tokens,
                error,
            }) => {
                let request = requests
                    .get_mut(&id)
                    .ok_or_else(|| anyhow::anyhow!("Request finish without start"))?;
                ensure!(
                    !request.1 && request.2 == event.turn_id,
                    "Duplicate or cross-turn request finish"
                );
                request.1 = true;
                if let Some(error) = error {
                    errors.push(error);
                }
                if !usage_seen.contains(&id) {
                    reports.push(UsageReport {
                        measurement_id: id.clone(),
                        source_event_id: format!("finish-{id}"),
                        scope: Scope::Request,
                        source: "provider".into(),
                        counter: Counter::Snapshot,
                        complete: input_tokens.is_some() && output_tokens.is_some(),
                        input_tokens,
                        output_tokens,
                        cache_read_tokens: None,
                        cache_write_tokens: None,
                        reasoning_tokens: None,
                        baseline_input: None,
                        baseline_output: None,
                    });
                }
            }
            Payload::Observation(ExecutionObservation::CompactionLifecycle { id, action }) => {
                if action == "start" {
                    ensure!(
                        compactions.insert(id, false).is_none(),
                        "Duplicate compaction start"
                    );
                } else {
                    ensure!(
                        matches!(action.as_str(), "complete" | "failed"),
                        "Unknown compaction action"
                    );
                    let done = compactions
                        .get_mut(&id)
                        .ok_or_else(|| anyhow::anyhow!("Compaction terminal without start"))?;
                    ensure!(!*done, "Duplicate compaction terminal");
                    *done = true;
                }
            }
            Payload::Usage(report) => {
                ensure!(
                    report.scope != Scope::Request || requests.contains_key(&report.measurement_id),
                    "Usage for unknown request"
                );
                reports.push(report);
            }
            _ => {}
        }
    }
    if !active_tools.is_empty() {
        errors.push("Unsettled tool calls".into());
    }
    if terminals != scenario.turns.len() {
        errors.push("Not all turns completed".into());
    }
    for (id, (_, done, _)) in &requests {
        if !done {
            errors.push("Unsettled model request".into());
            if !usage_seen.contains(id) {
                reports.push(UsageReport {
                    measurement_id: id.clone(),
                    source_event_id: format!("missing-{id}"),
                    scope: Scope::Request,
                    source: "provider".into(),
                    counter: Counter::Snapshot,
                    complete: false,
                    input_tokens: None,
                    output_tokens: None,
                    cache_read_tokens: None,
                    cache_write_tokens: None,
                    reasoning_tokens: None,
                    baseline_input: None,
                    baseline_output: None,
                });
            }
        }
    }
    if compactions.values().any(|done| !done) {
        errors.push("Unsettled compaction".into());
    }
    ensure!(
        scenario.turn_assertions.is_empty()
            || scenario.turn_assertions.len() == scenario.turns.len(),
        "Turn assertion count mismatch"
    );
    for (index, assertion) in scenario.turn_assertions.iter().enumerate() {
        let turn = format!("turn-{}", index + 1);
        if !turn_answers
            .get(&turn)
            .is_some_and(|text| assertion.accepts(text))
        {
            errors.push(format!("Output assertion failed: {turn}"));
        }
    }
    if let Some(expected) = scenario.expected_final_text
        && final_text != expected
    {
        errors.push("Final answer assertion failed".into());
    }
    let workspace = directory.join("workspace").canonicalize()?;
    for (name, expected) in scenario.expected_files {
        let path = relative(&workspace, &name)?;
        let actual = path.canonicalize();
        if !actual.as_ref().is_ok_and(|p| p.starts_with(&workspace))
            || fs::read_to_string(&path).ok().as_deref() != Some(&expected)
        {
            errors.push(format!("Artifact assertion failed: {name}"));
        }
    }
    // Validate each scope independently; only request coverage is selected here.
    aggregate(&reports, Scope::Turn)?;
    aggregate(&reports, Scope::Run)?;
    let usage = aggregate(&reports, Scope::Request)?;
    let mut stage_usage = BTreeMap::new();
    for stage in ["agent", "compaction"] {
        let selected = reports
            .iter()
            .filter(|r| {
                requests
                    .get(&r.measurement_id)
                    .is_some_and(|request| request.0 == stage)
            })
            .cloned()
            .collect::<Vec<_>>();
        stage_usage.insert(stage.into(), aggregate(&selected, Scope::Request)?);
    }
    let measurement_complete = usage.measurements == requests.len()
        && usage.input_tokens.is_some()
        && usage.output_tokens.is_some()
        && !requests.is_empty()
        && requests.values().all(|r| r.1);
    let mut turn_usage = BTreeMap::new();
    for index in 0..scenario.turns.len() {
        let turn = format!("turn-{}", index + 1);
        let selected = reports
            .iter()
            .filter(|r| {
                requests
                    .get(&r.measurement_id)
                    .is_some_and(|request| request.2 == turn)
            })
            .cloned()
            .collect::<Vec<_>>();
        turn_usage.insert(turn, aggregate(&selected, Scope::Request)?);
    }
    Ok(Report {
        schema_version: SCHEMA,
        run_id: manifest.run_id,
        scenario_id: manifest.scenario_id,
        scenario_hash: manifest.scenario_hash,
        mode: manifest.mode,
        success: errors.is_empty(),
        errors,
        usage,
        stage_usage,
        turn_usage,
        turn_answers,
        request_count: requests.len(),
        tool_count,
        compaction_count: compactions.len(),
        measurement_complete,
        transport_hash: manifest.transport.as_ref().map(hash).transpose()?,
    })
}

pub fn compare(baseline: &Report, candidate: &Report) -> Result<Value> {
    ensure!(
        baseline.schema_version == candidate.schema_version
            && baseline.scenario_hash == candidate.scenario_hash
            && baseline.mode == candidate.mode
            && baseline.transport_hash == candidate.transport_hash,
        "Incompatible run schema/scenario/mode"
    );
    let delta =
        |a: Option<u64>, b: Option<u64>| a.zip(b).map(|(a, b)| i128::from(b) - i128::from(a));
    let comparable = baseline.success
        && candidate.success
        && baseline.measurement_complete
        && candidate.measurement_complete;
    Ok(
        json!({"scenario_id":baseline.scenario_id,"mode":baseline.mode,"comparable":comparable,"empirical_efficiency_claim":false,"baseline_success":baseline.success,"candidate_success":candidate.success,"input_delta":if comparable {delta(baseline.usage.input_tokens,candidate.usage.input_tokens)} else {None},"output_delta":if comparable {delta(baseline.usage.output_tokens,candidate.usage.output_tokens)} else {None}}),
    )
}
