//! Calendar scheduling shares the task hub lock and its durable transaction.
//! A scheduled occurrence and its queue entry are committed together.
use super::*;
use chrono::{Datelike, Duration, NaiveDateTime, NaiveTime, TimeZone, Utc};
use chrono_tz::Tz;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(Clone, Default, Serialize, Deserialize)]
pub(super) struct Book {
    plans: BTreeMap<String, Plan>,
    receipts: BTreeMap<String, (Value, Value)>,
    outbox: Vec<Message>,
}
#[derive(Clone, Serialize, Deserialize)]
struct Plan {
    info: ScheduleInfo,
    generation: u64,
    runs: Vec<ScheduleRun>,
}
fn invalid(message: impl ToString) -> (StatusCode, Json<ApiError>) {
    error(
        StatusCode::BAD_REQUEST,
        "invalid_schedule",
        &message.to_string(),
    )
}
fn zone(spec: &ScheduleSpec) -> Result<Tz> {
    Ok(spec.timezone.parse()?)
}
fn instant(ms: i64) -> Result<chrono::DateTime<Utc>> {
    Utc.timestamp_millis_opt(ms)
        .single()
        .ok_or_else(|| anyhow!("时间超出范围"))
}
fn validate(spec: &ScheduleSpec) -> Result<()> {
    anyhow::ensure!(
        !spec.title.trim().is_empty() && spec.title.chars().count() <= 80,
        "标题需要 1–80 个字符"
    );
    anyhow::ensure!(
        !spec.goal.trim().is_empty() && spec.goal.len() <= 8000,
        "目标为空或超过 8000 字节"
    );
    zone(spec)?;
    anyhow::ensure!(
        ["coalesce", "skip"].contains(&spec.misfire.as_str()),
        "补跑策略无效"
    );
    anyhow::ensure!(
        ["changes", "all", "failures"].contains(&spec.notify.as_str()),
        "通知策略无效"
    );
    match spec.kind.as_str() {
        "once" => {
            once(spec)?;
        }
        "interval" => anyhow::ensure!(
            (1..=525600).contains(&spec.every_minutes),
            "间隔需要 1–525600 分钟"
        ),
        "daily" | "weekly" => {
            NaiveTime::parse_from_str(&spec.time, "%H:%M")?;
            if spec.kind == "weekly" {
                anyhow::ensure!(
                    !spec.weekdays.is_empty()
                        && spec.weekdays.len() <= 7
                        && spec.weekdays.iter().all(|d| (1..=7).contains(d)),
                    "每周计划需选择星期 1–7（周一至周日）"
                );
            }
        }
        _ => bail!("不支持的时间规则"),
    }
    Ok(())
}
fn once(spec: &ScheduleSpec) -> Result<i64> {
    let local = NaiveDateTime::parse_from_str(&spec.at, "%Y-%m-%d %H:%M")?;
    // Fall-back ambiguity fires only at the earlier instant. Spring gaps are rejected.
    zone(spec)?
        .from_local_datetime(&local)
        .earliest()
        .map(|t| t.timestamp_millis())
        .ok_or_else(|| anyhow!("该本地时间不存在（夏令时跳变）"))
}
fn next(spec: &ScheduleSpec, after: i64) -> Result<Option<i64>> {
    match spec.kind.as_str() {
        "once" => {
            let at = once(spec)?;
            Ok((at > after).then_some(at))
        }
        "interval" => Ok(Some(
            after
                .checked_add((spec.every_minutes as i64) * 60000)
                .ok_or_else(|| anyhow!("间隔溢出"))?,
        )),
        _ => {
            let tz = zone(spec)?;
            let date = instant(after)?.with_timezone(&tz).date_naive();
            let time = NaiveTime::parse_from_str(&spec.time, "%H:%M")?;
            for n in 0..=8 {
                let date = date
                    .checked_add_signed(Duration::days(n))
                    .ok_or_else(|| anyhow!("日期溢出"))?;
                if spec.kind == "weekly"
                    && !spec.weekdays.contains(&date.weekday().number_from_monday())
                {
                    continue;
                }
                if let Some(t) = tz.from_local_datetime(&date.and_time(time)).earliest()
                    && t.timestamp_millis() > after
                {
                    return Ok(Some(t.timestamp_millis()));
                }
            }
            bail!("无法计算下次运行")
        }
    }
}
fn after_due(spec: &ScheduleSpec, due: i64, at: i64) -> Result<Option<i64>> {
    if spec.kind == "once" {
        return Ok(None);
    }
    if spec.kind == "interval" {
        let step = spec.every_minutes as i64 * 60000;
        return Ok(Some(
            due.checked_add(((at - due) / step + 1) * step)
                .ok_or_else(|| anyhow!("时间溢出"))?,
        ));
    }
    next(spec, at)
}
fn info(plan: &Plan, summary: bool) -> ScheduleInfo {
    let mut i = plan.info.clone();
    i.next_label = i
        .next_at_ms
        .and_then(|v| instant(v).ok())
        .and_then(|v| {
            zone(&i.spec)
                .ok()
                .map(|z| v.with_timezone(&z).format("%Y-%m-%d %H:%M %Z").to_string())
        })
        .unwrap_or_default();
    if summary {
        i.spec.goal.clear();
        i.baseline = None;
    }
    i
}
#[derive(Default, Deserialize)]
pub struct QueryPage {
    #[serde(default)]
    pub offset: usize,
    #[serde(default)]
    pub selected: String,
    #[serde(default)]
    pub runs_offset: usize,
    #[serde(default)]
    pub scope: String,
    pub limit: Option<usize>,
}
pub(super) fn page(root: &Service, q: QueryPage) -> ApiResult<SchedulePage> {
    let limit = q.limit.unwrap_or(20);
    if !(1..=50).contains(&limit) || !["", "all", "enabled", "paused"].contains(&q.scope.as_str()) {
        return Err(invalid("无效的计划查询"));
    }
    let hub = root.tasks.lock().unwrap();
    let plans: Vec<_> = hub
        .schedules
        .plans
        .values()
        .filter(|p| {
            !p.info.deleted
                && match q.scope.as_str() {
                    "enabled" => p.info.spec.enabled,
                    "paused" => !p.info.spec.enabled,
                    _ => true,
                }
        })
        .collect();
    let total = plans.len();
    let offset = q.offset.min(total.saturating_sub(1) / limit * limit);
    let end = (offset + limit).min(total);
    let selected = hub.schedules.plans.get(&q.selected);
    let history: Vec<_> = selected
        .map(|p| {
            p.runs
                .iter()
                .rev()
                .skip(q.runs_offset)
                .take(limit)
                .map(|r| {
                    let mut r = r.clone();
                    r.scheduled_label = instant(r.scheduled_for_ms)
                        .ok()
                        .and_then(|at| {
                            selected.and_then(|p| zone(&p.info.spec).ok()).map(|tz| {
                                at.with_timezone(&tz)
                                    .format("%Y-%m-%d %H:%M %Z")
                                    .to_string()
                            })
                        })
                        .unwrap_or_default();
                    if let Some(t) = &r.task_id
                        && r.finished_at_ms.is_none()
                        && let Some(p) = tasks::task_phase(&hub, t)
                    {
                        r.state = serde_json::to_value(p).unwrap().as_str().unwrap().into();
                    }
                    r
                })
                .collect()
        })
        .unwrap_or_default();
    let next_run = selected.and_then(|p| {
        ((q.runs_offset + history.len()) < p.runs.len()).then_some(q.runs_offset + history.len())
    });
    Ok(SchedulePage {
        items: plans[offset..end].iter().map(|p| info(p, true)).collect(),
        total,
        offset,
        next_offset: (end < total).then_some(end),
        selected: selected.map(|p| info(p, false)),
        runs: history,
        runs_next_offset: next_run,
    })
}
pub async fn list(
    State(root): State<Service>,
    h: HeaderMap,
    Query(q): Query<QueryPage>,
) -> ApiResult<Json<SchedulePage>> {
    root.authorized(&h)?;
    Ok(Json(page(&root, q)?))
}
pub async fn command(
    State(root): State<Service>,
    h: HeaderMap,
    Json(req): Json<Value>,
) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    Ok(Json(apply(&root, req, now())?))
}
pub(super) fn apply(root: &Service, req: Value, at: i64) -> ApiResult<Value> {
    let key = req["request_id"]
        .as_str()
        .filter(|s| !s.is_empty() && s.len() <= 128)
        .ok_or_else(|| invalid("缺少请求编号"))?;
    let action = req["action"].as_str().ok_or_else(|| invalid("缺少操作"))?;
    let mut hub = root.tasks.lock().unwrap();
    if let Some((old, result)) = hub.schedules.receipts.get(key) {
        if old != &req {
            return Err(error(
                StatusCode::CONFLICT,
                "request_conflict",
                "请求编号已用于其他内容",
            ));
        }
        return Ok(result.clone());
    }
    let before = hub.schedules.clone();
    let mut inserted = None;
    let result = (|| -> ApiResult<Value> {
        if action == "create" {
            if !agent::configured(root) {
                return Err(invalid("定时任务需要主 Agent 配置"));
            }
            if hub
                .schedules
                .plans
                .values()
                .filter(|p| !p.info.deleted)
                .count()
                >= 100
            {
                return Err(invalid("最多 100 个计划"));
            }
            let spec: ScheduleSpec =
                serde_json::from_value(req["spec"].clone()).map_err(invalid)?;
            validate(&spec).map_err(invalid)?;
            agent::routing::resolve(
                root,
                spec.profile.as_deref(),
                agent::routing::Source::Schedule,
                &spec.requirements,
            )
            .map_err(invalid)?;
            let due = if spec.enabled {
                next(&spec, at).map_err(invalid)?
            } else {
                None
            };
            if spec.enabled && due.is_none() {
                return Err(invalid("一次性计划的时间必须在未来"));
            }
            let plan = Plan {
                info: ScheduleInfo {
                    id: id(),
                    spec,
                    next_at_ms: due,
                    next_label: String::new(),
                    deleted: false,
                    last_error: String::new(),
                    baseline: None,
                    created_at_ms: at,
                },
                generation: 0,
                runs: Vec::new(),
            };
            let result = json!(info(&plan, false));
            hub.schedules.plans.insert(plan.info.id.clone(), plan);
            return Ok(result);
        }
        let sid = req["schedule_id"]
            .as_str()
            .ok_or_else(|| invalid("缺少计划编号"))?
            .to_string();
        if !hub.schedules.plans.contains_key(&sid) {
            return Err(error(
                StatusCode::NOT_FOUND,
                "schedule_missing",
                "计划不存在",
            ));
        }
        if hub.schedules.plans[&sid].info.deleted {
            return Err(invalid("计划已删除"));
        }
        if action == "run" {
            if busy(&hub, &sid) {
                return Err(error(
                    StatusCode::CONFLICT,
                    "schedule_busy",
                    "该计划已有排队或运行中的任务",
                ));
            }
            let task = launch(root, &mut hub, &sid, at, format!("manual:{key}"))?;
            inserted = Some(task.clone());
            return Ok(json!({"task_id":task}));
        }
        let plan = hub.schedules.plans.get_mut(&sid).unwrap();
        match action {
            "update" => {
                let spec: ScheduleSpec =
                    serde_json::from_value(req["spec"].clone()).map_err(invalid)?;
                validate(&spec).map_err(invalid)?;
                agent::routing::resolve(
                    root,
                    spec.profile.as_deref(),
                    agent::routing::Source::Schedule,
                    &spec.requirements,
                )
                .map_err(invalid)?;
                let due = if spec.enabled {
                    next(&spec, at).map_err(invalid)?
                } else {
                    None
                };
                if spec.enabled && due.is_none() {
                    return Err(invalid("一次性计划时间已过"));
                }
                if spec.goal != plan.info.spec.goal {
                    plan.generation += 1;
                    plan.info.baseline = None;
                }
                plan.info.spec = spec;
                plan.info.next_at_ms = due;
                plan.info.last_error.clear();
            }
            "pause" => {
                plan.info.spec.enabled = false;
                plan.info.next_at_ms = None;
            }
            "resume" => {
                let due = next(&plan.info.spec, at)
                    .map_err(invalid)?
                    .ok_or_else(|| invalid("一次性计划已过期，请编辑时间"))?;
                plan.info.spec.enabled = true;
                plan.info.next_at_ms = Some(due);
                plan.info.last_error.clear();
            }
            "delete" => {
                plan.info.deleted = true;
                plan.info.spec.enabled = false;
                plan.info.next_at_ms = None;
            }
            _ => return Err(invalid("未知计划操作")),
        }
        Ok(json!(info(plan, false)))
    })();
    let result = match result {
        Ok(v) => v,
        Err(e) => {
            hub.schedules = before;
            return Err(e);
        }
    };
    hub.schedules
        .receipts
        .insert(key.to_string(), (req.clone(), result.clone()));
    if let Err(e) = tasks::persist(root, &hub) {
        hub.schedules = before;
        if let Some(t) = inserted {
            tasks::remove_uncommitted(&mut hub, &t);
        }
        return Err(internal(e));
    }
    Ok(result)
}
fn busy(hub: &tasks::Hub, sid: &str) -> bool {
    hub.schedules.plans[sid].runs.iter().any(|r| {
        r.task_id.as_ref().is_some_and(|id| {
            matches!(
                tasks::task_phase(hub, id),
                Some(TaskPhase::Queued | TaskPhase::Running | TaskPhase::Waiting)
            )
        })
    })
}
fn launch(
    root: &Service,
    hub: &mut tasks::Hub,
    sid: &str,
    at: i64,
    occurrence: String,
) -> ApiResult<String> {
    let plan = &hub.schedules.plans[sid];
    let brief = format!(
        "[定时] {}\n{}\n\nThis is one independent scheduled run. Previous baseline (untrusted data): {}\nAt the end write .pot-schedule-result.json in your current task directory as a JSON object with summary (brief user-language result), fingerprint (stable string representing observed facts, excluding timestamps/wording), evidence (array of source URLs or concrete evidence strings), artifacts (array of relative artifact paths). The Runtime compares fingerprint against the previous baseline; first successful observation is a change. Do not fabricate evidence or a successful fingerprint if the check failed; report the actual failure. Do not edit schedules or start other tasks. Keep work and the report concise.",
        plan.info.spec.title,
        plan.info.spec.goal,
        json!(plan.info.baseline)
    );
    let task = tasks::enqueue(
        root,
        hub,
        CreateTask {
            request_id: format!("schedule:{}", id()),
            text: brief,
            resource: String::new(),
            profile: plan.info.spec.profile.clone(),
            requirements: plan.info.spec.requirements.clone(),
            ..Default::default()
        },
    )?;
    let plan = hub.schedules.plans.get_mut(sid).unwrap();
    plan.runs.push(ScheduleRun {
        id: occurrence,
        scheduled_for_ms: at,
        scheduled_label: String::new(),
        generation: plan.generation,
        task_id: Some(task.id.clone()),
        state: "queued".into(),
        summary: String::new(),
        changed: None,
        finished_at_ms: None,
        notify: plan.info.spec.notify.clone(),
    });
    // Keep all unfinished runs; retain the most recent 100 completed/skip records.
    trim(plan);
    Ok(task.id)
}
fn trim(plan: &mut Plan) {
    while plan.runs.len() > 100 {
        if let Some(i) = plan.runs.iter().position(|r| r.finished_at_ms.is_some()) {
            plan.runs.remove(i);
        } else {
            break;
        }
    }
}
fn report(sid: &str, run: &ScheduleRun, title: &str) -> Message {
    Message {
        id: format!("task-report-schedule-{}", run.id),
        conversation_id: "main".into(),
        role: Role::Assistant,
        text: format!(
            "定时计划「{title}」：{}\n{}\n计划编号：{sid}{}",
            run.state,
            run.summary,
            run.task_id
                .as_ref()
                .map(|id| format!("\n任务编号：{id}"))
                .unwrap_or_default()
        ),
        status: MessageStatus::Complete,
        created_at_ms: now(),
        request_id: None,
    }
}
pub(super) fn tick(root: &Service, at: i64) -> Result<()> {
    let mut hub = root.tasks.lock().unwrap();
    // Reconcile a cancelled queued task or a worker interrupted across restart.
    let recovery_before = hub.schedules.clone();
    let mut changed = false;
    let mut recovered = Vec::new();
    for (sid, p) in &hub.schedules.plans {
        for (index, r) in p
            .runs
            .iter()
            .enumerate()
            .filter(|(_, r)| r.finished_at_ms.is_none())
        {
            if let Some(task) = &r.task_id
                && let Some(phase) = tasks::task_phase(&hub, task)
                && !matches!(
                    phase,
                    TaskPhase::Running | TaskPhase::Queued | TaskPhase::Waiting
                )
            {
                recovered.push((sid.clone(), index, phase));
            }
        }
    }
    for (sid, index, phase) in recovered {
        let p = hub.schedules.plans.get_mut(&sid).unwrap();
        let r = &mut p.runs[index];
        r.state = serde_json::to_value(&phase)?.as_str().unwrap().into();
        r.finished_at_ms = Some(at);
        r.summary = "执行已取消或中断；未自动重放。".into();
        if r.notify == "all"
            || (phase != TaskPhase::Cancelled
                && ["changes", "failures"].contains(&r.notify.as_str()))
        {
            let message = report(&sid, r, &p.info.spec.title);
            hub.schedules.outbox.push(message);
        }
        changed = true;
    }
    if changed && let Err(error) = tasks::persist(root, &hub) {
        hub.schedules = recovery_before;
        return Err(error);
    }
    // Flush durable notifications with their removal in the same transaction.
    if let Some(message) = hub.schedules.outbox.first().cloned() {
        hub.schedules.outbox.remove(0);
        let serialized = tasks::serialized(&hub)?;
        let saved = root
            .inner
            .lock()
            .unwrap()
            .store
            .save_with_metadata(message.clone(), Some(("tasks.v2", &serialized)));
        match saved {
            Ok(e) => {
                let _ = root.events.send(e);
            }
            Err(e) => {
                hub.schedules.outbox.insert(0, message);
                return Err(e);
            }
        }
    }
    if !agent::configured(root)
        || root.inner.lock().unwrap().engine.phase != EnginePhase::Ready
        || root.budget()?.is_some_and(|b| b.remaining() == 0)
    {
        return Ok(());
    }
    let due: Vec<_> = hub
        .schedules
        .plans
        .iter()
        .filter(|(_, p)| {
            !p.info.deleted && p.info.spec.enabled && p.info.next_at_ms.is_some_and(|d| d <= at)
        })
        .map(|(sid, _)| sid.clone())
        .collect();
    for sid in due {
        let before = hub.schedules.clone();
        let scheduled = hub.schedules.plans[&sid].info.next_at_ms.unwrap();
        let next_at = after_due(&hub.schedules.plans[&sid].info.spec, scheduled, at)?;
        let overlap = busy(&hub, &sid);
        let missed = at.saturating_sub(scheduled) >= 60000
            && hub.schedules.plans[&sid].info.spec.misfire == "skip";
        let mut inserted = None;
        if overlap || missed {
            let p = hub.schedules.plans.get_mut(&sid).unwrap();
            p.runs.push(ScheduleRun {
                id: format!("{sid}:{scheduled}"),
                scheduled_for_ms: scheduled,
                scheduled_label: String::new(),
                generation: p.generation,
                task_id: None,
                state: if overlap {
                    "skipped_overlap"
                } else {
                    "skipped_missed"
                }
                .into(),
                summary: "本次未创建任务，也未调用模型。".into(),
                changed: None,
                finished_at_ms: Some(at),
                notify: p.info.spec.notify.clone(),
            });
            trim(p);
        } else {
            match launch(
                root,
                &mut hub,
                &sid,
                scheduled,
                format!("{sid}:{scheduled}"),
            ) {
                Ok(task) => inserted = Some(task),
                Err(e) => {
                    hub.schedules = before;
                    let p = hub.schedules.plans.get_mut(&sid).unwrap();
                    let detail = e.1.0.message;
                    if p.info.last_error != detail {
                        p.info.last_error = detail;
                        tasks::persist(root, &hub)?;
                    }
                    continue;
                }
            }
        }
        let p = hub.schedules.plans.get_mut(&sid).unwrap();
        p.info.next_at_ms = next_at;
        if p.info.next_at_ms.is_none() {
            p.info.spec.enabled = false;
        }
        p.info.last_error.clear();
        if let Err(e) = tasks::persist(root, &hub) {
            hub.schedules = before;
            if let Some(t) = inserted {
                tasks::remove_uncommitted(&mut hub, &t);
            }
            return Err(e);
        }
    }
    Ok(())
}
#[derive(Deserialize)]
struct ResultFile {
    summary: String,
    fingerprint: String,
    evidence: Vec<String>,
    #[serde(default)]
    artifacts: Vec<String>,
}
fn read_result(workspace: &std::path::Path) -> Result<ResultFile> {
    let path = workspace.join(".pot-schedule-result.json");
    let metadata = std::fs::symlink_metadata(&path)?;
    anyhow::ensure!(
        metadata.is_file() && metadata.len() <= 16384,
        "结果文件必须是小于 16 KiB 的普通文件"
    );
    let r: ResultFile = serde_json::from_slice(&std::fs::read(path)?)?;
    anyhow::ensure!(
        !r.fingerprint.trim().is_empty()
            && r.fingerprint.len() <= 2048
            && !r.summary.trim().is_empty()
            && r.summary.len() <= 4000
            && !r.evidence.is_empty()
            && r.evidence.len() <= 16
            && r.artifacts.len() <= 16
            && r.evidence
                .iter()
                .chain(&r.artifacts)
                .all(|s| s.len() <= 1000),
        "结果需要简短 summary、稳定 fingerprint 和 evidence"
    );
    Ok(r)
}
/// None = ordinary task. Some(None) = scheduled run with a quiet result.
pub(super) fn complete(
    book: &mut Book,
    task: &str,
    status: &mut TaskPhase,
    workspace: &std::path::Path,
    answer: &str,
    at: i64,
) -> Option<Option<String>> {
    for p in book.plans.values_mut() {
        let Some(r) = p
            .runs
            .iter_mut()
            .find(|r| r.task_id.as_deref() == Some(task) && r.finished_at_ms.is_none())
        else {
            continue;
        };
        r.finished_at_ms = Some(at);
        if *status == TaskPhase::Complete {
            match read_result(workspace) {
                Ok(result) => {
                    r.changed = Some(p.info.baseline.as_deref() != Some(&result.fingerprint));
                    r.summary = format!(
                        "{}\n证据：{}\n产物：{}",
                        result.summary,
                        result.evidence.join("; "),
                        result.artifacts.join(", ")
                    );
                    if r.generation == p.generation {
                        p.info.baseline = Some(result.fingerprint);
                    }
                }
                Err(e) => {
                    *status = TaskPhase::Failed;
                    r.summary = format!(
                        "结构化结果缺失或无效：{e}。原始回复：{}",
                        answer.chars().take(400).collect::<String>()
                    );
                }
            }
        } else {
            r.summary = answer.chars().take(1200).collect();
        }
        r.state = serde_json::to_value(&status)
            .unwrap()
            .as_str()
            .unwrap()
            .into();
        let failed = matches!(status, TaskPhase::Failed | TaskPhase::Interrupted);
        let notify = r.notify == "all"
            || (r.notify == "changes" && (r.changed == Some(true) || failed))
            || (r.notify == "failures" && failed);
        return Some(notify.then(|| {
            format!(
                "定时计划「{}」：{}\n{}",
                p.info.spec.title, r.state, r.summary
            )
        }));
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;
    fn ms(s: &str) -> i64 {
        chrono::DateTime::parse_from_rfc3339(s)
            .unwrap()
            .timestamp_millis()
    }
    fn spec() -> ScheduleSpec {
        serde_json::from_value(json!({"title":"Test monitor","goal":"Check fixture source","timezone":"Asia/Shanghai","kind":"interval","every_minutes":1})).unwrap()
    }
    fn create(s: &Service, spec: ScheduleSpec, at: i64) -> String {
        apply(
            s,
            json!({"request_id":id(),"action":"create","spec":spec}),
            at,
        )
        .unwrap()["id"]
            .as_str()
            .unwrap()
            .into()
    }
    fn action(s: &Service, sid: &str, action: &str, at: i64) -> Value {
        apply(
            s,
            json!({"request_id":id(),"action":action,"schedule_id":sid}),
            at,
        )
        .unwrap()
    }
    fn result(worker: &Service, fingerprint: &str) {
        execution::private_json(&worker.workspace.join(".pot-schedule-result.json"),&json!({"summary":"Verified fixture observation","fingerprint":fingerprint,"evidence":["fixture://source"],"artifacts":[]})).unwrap();
    }
    #[test]
    fn timezone_weekday_dst_and_interval_are_deterministic() {
        let mut p = spec();
        p.kind = "daily".into();
        p.time = "09:00".into();
        assert_eq!(
            next(&p, ms("2026-10-04T00:00:00Z")).unwrap(),
            Some(ms("2026-10-04T01:00:00Z"))
        );
        p.kind = "weekly".into();
        p.weekdays = vec![1, 2, 3, 4, 5];
        assert_eq!(
            next(&p, ms("2026-10-02T02:00:00Z")).unwrap(),
            Some(ms("2026-10-05T01:00:00Z"))
        );
        p.timezone = "America/New_York".into();
        p.kind = "daily".into();
        p.time = "02:30".into();
        assert_eq!(
            next(&p, ms("2026-03-08T05:00:00Z")).unwrap(),
            Some(ms("2026-03-09T06:30:00Z"))
        );
        p.time = "01:30".into();
        assert_eq!(
            next(&p, ms("2026-11-01T04:00:00Z")).unwrap(),
            Some(ms("2026-11-01T05:30:00Z"))
        );
        assert_eq!(
            next(&p, ms("2026-11-01T05:30:00Z")).unwrap(),
            Some(ms("2026-11-02T06:30:00Z"))
        );
        p.kind = "once".into();
        p.at = "2026-03-08 02:30".into();
        assert!(validate(&p).is_err());
        p = spec();
        assert_eq!(after_due(&p, 0, 600000).unwrap(), Some(660000));
        p.every_minutes = 0;
        assert!(validate(&p).is_err());
        p = spec();
        p.timezone = "Mars/Olympus".into();
        assert!(validate(&p).is_err());
    }
    #[test]
    fn occurrence_is_atomic_deduplicated_nonoverlapping_and_recoverable() {
        let s = tasks::tests::fixture();
        let sid = create(&s, spec(), 0);
        tick(&s, 600000).unwrap();
        tick(&s, 600000).unwrap();
        assert_eq!(s.tasks.lock().unwrap().schedules.plans[&sid].runs.len(), 1);
        assert_eq!(tasks::page(&s, &TaskQuery::default()).unwrap().total, 1);
        tick(&s, 660000).unwrap();
        assert_eq!(
            s.tasks.lock().unwrap().schedules.plans[&sid].runs[1].state,
            "skipped_overlap"
        );
        let a = tasks::claim(&s).unwrap().unwrap();
        action(&s, &sid, "pause", 660000);
        assert!(!a.1.snapshot().unwrap().turn.unwrap().cancelling);
        assert!(
            apply(
                &s,
                json!({"request_id":id(),"action":"run","schedule_id":sid}),
                660000
            )
            .is_err()
        );
        // Restart marks the active task interrupted; scheduler reconciles and does not replay it.
        let mut reopened = s.clone();
        reopened.tasks = Arc::new(Mutex::new(tasks::Hub::default()));
        tasks::restore(&reopened).unwrap();
        tick(&reopened, 900000).unwrap();
        let hub = reopened.tasks.lock().unwrap();
        assert_eq!(hub.schedules.plans[&sid].runs[0].state, "interrupted");
        assert!(hub.schedules.plans[&sid].runs[0].finished_at_ms.is_some());
        drop(hub);
        assert!(tasks::claim(&reopened).unwrap().is_none());
        assert_eq!(
            reopened
                .snapshot()
                .unwrap()
                .messages
                .iter()
                .filter(|m| m.id.starts_with("task-report-"))
                .count(),
            1
        );
        tick(&reopened, 900000).unwrap();
        assert_eq!(reopened.snapshot().unwrap().messages.len(), 1);
        action(&reopened, &sid, "resume", 900000);
        assert_eq!(
            reopened.tasks.lock().unwrap().schedules.plans[&sid]
                .info
                .next_at_ms,
            Some(960000)
        );
        action(&reopened, &sid, "delete", 900000);
        assert!(
            page(&reopened, QueryPage::default())
                .unwrap()
                .items
                .is_empty()
        );
    }
    #[test]
    fn skip_misfires_once_and_manual_receipts() {
        let s = tasks::tests::fixture();
        let mut p = spec();
        p.misfire = "skip".into();
        let sid = create(&s, p, 0);
        tick(&s, 600000).unwrap();
        assert_eq!(
            s.tasks.lock().unwrap().schedules.plans[&sid].runs[0].state,
            "skipped_missed"
        );
        assert_eq!(tasks::page(&s, &TaskQuery::default()).unwrap().total, 0);
        action(&s, &sid, "pause", 600000);
        let req = json!({"request_id":"manual-test","action":"run","schedule_id":sid});
        let first = apply(&s, req.clone(), 600000).unwrap();
        assert_eq!(first, apply(&s, req, 610000).unwrap());
        assert_eq!(tasks::page(&s, &TaskQuery::default()).unwrap().total, 1);
        assert!(
            apply(
                &s,
                json!({"request_id":"manual-test","action":"delete","schedule_id":sid}),
                610000
            )
            .is_err()
        );
        let mut once = spec();
        once.kind = "once".into();
        once.at = "2026-10-05 09:00".into();
        let at = ms("2026-10-05T00:00:00Z");
        let id = create(&s, once, at);
        tick(&s, ms("2026-10-05T01:00:01Z")).unwrap();
        assert!(
            !s.tasks.lock().unwrap().schedules.plans[&id]
                .info
                .spec
                .enabled
        );
        assert_eq!(s.tasks.lock().unwrap().schedules.plans[&id].runs.len(), 1);
        tick(&s, ms("2026-10-06T01:00:00Z")).unwrap();
        assert_eq!(s.tasks.lock().unwrap().schedules.plans[&id].runs.len(), 1);
    }
    #[test]
    fn changes_are_quiet_and_failures_are_visible_without_advancing_baseline() {
        let s = tasks::tests::fixture();
        let sid = create(&s, spec(), 0);
        for (at, fingerprint, reports) in [(60000, "a", 1), (120000, "a", 1), (180000, "b", 2)] {
            tick(&s, at).unwrap();
            let a = tasks::claim(&s).unwrap().unwrap();
            result(&a.1, fingerprint);
            tasks::tests::succeed(&s, &a.0, &a.1, &a.2);
            assert_eq!(s.snapshot().unwrap().messages.len(), reports);
        }
        tick(&s, 240000).unwrap();
        let a = tasks::claim(&s).unwrap().unwrap();
        tasks::tests::succeed(&s, &a.0, &a.1, &a.2); // Missing report is not silently "no change".
        assert_eq!(
            tasks::task_phase(&s.tasks.lock().unwrap(), &a.0),
            Some(TaskPhase::Failed)
        );
        assert_eq!(s.snapshot().unwrap().messages.len(), 3);
        let hub = s.tasks.lock().unwrap();
        assert_eq!(
            hub.schedules.plans[&sid].info.baseline.as_deref(),
            Some("b")
        );
        assert_eq!(hub.schedules.plans[&sid].runs[1].changed, Some(false));
    }
    #[test]
    fn generation_changes_cannot_replace_new_baseline_and_failed_commits_do_not_dispatch() {
        let s = tasks::tests::fixture();
        let sid = create(&s, spec(), 0);
        tick(&s, 60000).unwrap();
        let a = tasks::claim(&s).unwrap().unwrap();
        let mut p = spec();
        p.goal = "different observation".into();
        apply(
            &s,
            json!({"request_id":id(),"action":"update","schedule_id":sid,"spec":p}),
            70000,
        )
        .unwrap();
        result(&a.1, "old-goal");
        tasks::tests::succeed(&s, &a.0, &a.1, &a.2);
        assert!(
            s.tasks.lock().unwrap().schedules.plans[&sid]
                .info
                .baseline
                .is_none()
        );
        s.inner
            .lock()
            .unwrap()
            .store
            .0
            .execute_batch("PRAGMA query_only=ON;")
            .unwrap();
        assert!(tick(&s, 130000).is_err());
        assert!(tasks::claim(&s).unwrap().is_none());
        assert_eq!(
            s.tasks.lock().unwrap().schedules.plans[&sid]
                .info
                .next_at_ms,
            Some(130000)
        );
    }
    #[test]
    fn legacy_task_metadata_survives_schedule_migration() {
        let s = tasks::tests::fixture();
        s.inner.lock().unwrap().store.set("tasks.v1", "[]").unwrap();
        tasks::restore(&s).unwrap();
        assert!(
            s.inner
                .lock()
                .unwrap()
                .store
                .get("tasks.v2")
                .unwrap()
                .is_some()
        );
        create(&s, spec(), 0);
        let mut next = s.clone();
        next.tasks = Arc::new(Mutex::new(tasks::Hub::default()));
        tasks::restore(&next).unwrap();
        assert_eq!(page(&next, QueryPage::default()).unwrap().total, 1);
    }
}
