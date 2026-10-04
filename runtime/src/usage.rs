//! One shared, request-level ledger for the main agent and every task.
use super::*;
use chrono::{Datelike, Days, TimeZone, Utc};
use chrono_tz::Tz;
use rusqlite::{Connection, params};
use serde::Serialize;

pub struct Ledger(pub Connection);
#[derive(Default, Clone, Serialize, Deserialize, PartialEq)]
pub struct Totals {
    pub input: u64,
    pub output: u64,
    pub cache: u64,
    pub requests: u64,
    pub unknown: u64,
}
#[derive(Deserialize)]
pub struct QueryArgs {
    #[serde(default = "default_days")]
    days: u32,
    #[serde(default = "default_zone")]
    timezone: String,
    #[serde(default)]
    scope: String,
    #[serde(default)]
    offset: usize,
}
fn default_days() -> u32 {
    7
}
fn default_zone() -> String {
    "Asia/Shanghai".into()
}
#[derive(Clone, Serialize, Deserialize)]
pub struct Policy {
    pub period: String,
    pub limit: u64,
    pub timezone: String,
    pub auto_resume: bool,
}
impl Default for Policy {
    fn default() -> Self {
        Self {
            period: "none".into(),
            limit: 2_000_000,
            timezone: default_zone(),
            auto_resume: true,
        }
    }
}
impl Policy {
    fn validate(&self) -> Result<()> {
        anyhow::ensure!(
            matches!(self.period.as_str(), "none" | "day" | "week"),
            "Invalid period"
        );
        anyhow::ensure!(
            (1..=9_007_199_254_740_991).contains(&self.limit),
            "Invalid token limit"
        );
        self.timezone.parse::<Tz>()?;
        Ok(())
    }
    fn window(&self, at: i64) -> Result<(i64, i64)> {
        let tz: Tz = self.timezone.parse()?;
        let date = Utc
            .timestamp_millis_opt(at)
            .single()
            .ok_or_else(|| anyhow!("Invalid time"))?
            .with_timezone(&tz)
            .date_naive();
        let start = if self.period == "week" {
            date.checked_sub_days(Days::new(date.weekday().num_days_from_monday() as u64))
                .unwrap()
        } else {
            date
        };
        let end = start
            .checked_add_days(Days::new(if self.period == "week" { 7 } else { 1 }))
            .unwrap();
        Ok((midnight(tz, start)?, midnight(tz, end)?))
    }
}
fn midnight(tz: Tz, day: chrono::NaiveDate) -> Result<i64> {
    // Some timezones skip midnight at a DST transition. Find the first valid minute.
    for minute in 0..1440 {
        let naive = day.and_hms_opt(minute / 60, minute % 60, 0).unwrap();
        if let Some(t) = tz.from_local_datetime(&naive).earliest() {
            return Ok(t.timestamp_millis());
        }
    }
    bail!("Date does not exist in timezone")
}
impl Ledger {
    pub fn route(&self, request: &str, provider: &str, epoch: u64, spec_hash: &str) -> Result<()> {
        self.0.execute(
            "INSERT INTO request_routes(request_id,provider,epoch,spec_hash) VALUES (?,?,?,?)",
            params![request, provider, epoch, spec_hash],
        )?;
        Ok(())
    }

    pub fn open(path: &std::path::Path) -> Result<Self> {
        let c = Connection::open(path)?;
        c.execute_batch("PRAGMA journal_mode=DELETE; PRAGMA synchronous=EXTRA;
        CREATE TABLE IF NOT EXISTS requests(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL,model TEXT NOT NULL,input INTEGER,output INTEGER,cache INTEGER NOT NULL DEFAULT 0,reserved INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending');
        CREATE INDEX IF NOT EXISTS usage_time ON requests(at);
        CREATE INDEX IF NOT EXISTS usage_scope ON requests(scope,at);
        CREATE TABLE IF NOT EXISTS tools(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,name TEXT NOT NULL,at INTEGER NOT NULL,end_at INTEGER,bytes INTEGER,failed INTEGER NOT NULL DEFAULT 0);
        CREATE INDEX IF NOT EXISTS tools_scope ON tools(scope,at);
        CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
        CREATE TABLE IF NOT EXISTS waits(scope TEXT PRIMARY KEY,turn_id TEXT NOT NULL,at INTEGER NOT NULL,reset_at INTEGER NOT NULL,required INTEGER NOT NULL,released INTEGER NOT NULL DEFAULT 0);")?;
        c.execute_batch("CREATE TABLE IF NOT EXISTS request_routes(request_id TEXT PRIMARY KEY,provider TEXT NOT NULL,epoch INTEGER NOT NULL,spec_hash TEXT NOT NULL);")?;
        c.execute_batch("CREATE TABLE IF NOT EXISTS compactions(id TEXT PRIMARY KEY, scope TEXT NOT NULL, turn_id TEXT NOT NULL, at INTEGER NOT NULL, end_at INTEGER, reason TEXT NOT NULL, state TEXT NOT NULL DEFAULT 'running', tokens_before INTEGER, tokens_after INTEGER, entry_id TEXT);
        CREATE INDEX IF NOT EXISTS compaction_scope ON compactions(scope,at);")?;
        let has_column = c
            .prepare("PRAGMA table_info(requests)")?
            .query_map([], |r| r.get::<_, String>(1))?
            .collect::<rusqlite::Result<Vec<_>>>()?
            .iter()
            .any(|name| name == "compaction_id");
        if !has_column {
            c.execute_batch("ALTER TABLE requests ADD COLUMN compaction_id TEXT;")?;
        }
        c.execute_batch(
            "CREATE INDEX IF NOT EXISTS request_compaction ON requests(compaction_id);
            UPDATE compactions SET state='interrupted' WHERE state='running';",
        )?;
        // Unknown interrupted requests retain their reservations for this window.
        c.execute(
            "UPDATE requests SET state='unknown' WHERE state='pending'",
            [],
        )?;
        Ok(Self(c))
    }
    pub fn import_legacy(&self, scope: &str, store: &Store) -> Result<()> {
        if let Some(raw) = store.get("pi.usage")? {
            let values: Vec<Value> = serde_json::from_str(&raw)?;
            for (i, value) in values.iter().enumerate() {
                let key = format!("legacy:{scope}:{i}");
                let inserted=self.0.execute("INSERT OR IGNORE INTO requests(id,scope,turn_id,at,model,state) VALUES (?,?, 'legacy',0,'DeepSeek (历史)','legacy')",params![key,scope])?;
                if inserted > 0 {
                    self.settle(&key, value)?;
                }
            }
        }
        Ok(())
    }
    pub fn policy(&self) -> Result<Policy> {
        use rusqlite::OptionalExtension;
        let s: Option<String> = self
            .0
            .query_row("SELECT value FROM settings WHERE key='policy'", [], |r| {
                r.get(0)
            })
            .optional()?;
        Ok(s.map(|s| serde_json::from_str(&s))
            .transpose()?
            .unwrap_or_default())
    }
    pub fn set_policy(&self, p: &Policy) -> Result<()> {
        p.validate()?;
        self.0.execute("INSERT INTO settings VALUES ('policy',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",[serde_json::to_string(p)?])?;
        Ok(())
    }
    pub fn quota(&self, at: i64) -> Result<Value> {
        let p = self.policy()?;
        if p.period == "none" {
            return Ok(json!({"policy":p,"active":false}));
        }
        let (start, end) = p.window(at)?;
        let (used,reserved):(u64,u64)=self.0.query_row("SELECT COALESCE(SUM(COALESCE(input,0)+COALESCE(output,0)),0),COALESCE(SUM(reserved),0) FROM requests WHERE at>=? AND at<?",params![start,end],|r|Ok((r.get(0)?,r.get(1)?)))?;
        Ok(
            json!({"policy":p,"active":true,"used":used,"reserved":reserved,"reset_at":end,"reset_label":Utc.timestamp_millis_opt(end).single().unwrap().with_timezone(&p.timezone.parse::<Tz>()?).format("%Y-%m-%d %H:%M %Z").to_string(),"remaining":p.limit.saturating_sub(used.saturating_add(reserved))}),
        )
    }
    // Called under the shared ledger mutex: every lane observes the same reservation.
    pub fn reserve(
        &self,
        id: &str,
        scope: &str,
        turn: &str,
        at: i64,
        model: &str,
        input_estimate: u64,
        max_output: u64,
    ) -> Result<Option<u64>> {
        let q = self.quota(at)?;
        let output = if q["active"] == true {
            let available = q["remaining"]
                .as_u64()
                .unwrap_or(0)
                .saturating_sub(input_estimate);
            if available == 0 {
                return Ok(None);
            }
            max_output.min(available)
        } else {
            max_output
        };
        self.0.execute(
            "INSERT INTO requests(id,scope,turn_id,at,model,reserved) VALUES (?,?,?,?,?,?)",
            params![
                id,
                scope,
                turn,
                at,
                model,
                input_estimate.saturating_add(output)
            ],
        )?;
        self.0.execute("DELETE FROM waits WHERE scope=?", [scope])?;
        Ok(Some(output))
    }
    pub fn settle(&self, id: &str, u: &Value) -> Result<()> {
        let input = u["prompt_tokens"].as_u64();
        let output = u["completion_tokens"].as_u64();
        let cache = u["prompt_cache_hit_tokens"]
            .as_u64()
            .or_else(|| u["prompt_tokens_details"]["cached_tokens"].as_u64())
            .unwrap_or(0)
            .min(input.unwrap_or(0));
        anyhow::ensure!(
            input.unwrap_or(0) <= i64::MAX as u64 && output.unwrap_or(0) <= i64::MAX as u64,
            "Invalid usage"
        );
        self.0.execute("UPDATE requests SET input=COALESCE(?,input),output=COALESCE(?,output),cache=MAX(cache,?),reserved=CASE WHEN ? THEN 0 ELSE reserved END,state='reported' WHERE id=?",params![input,output,cache,input.is_some()&&output.is_some(),id])?;
        Ok(())
    }
    pub fn rejected(&self, id: &str) -> Result<()> {
        self.0.execute(
            "UPDATE requests SET reserved=0,state='rejected' WHERE id=?",
            [id],
        )?;
        Ok(())
    }
    pub fn wait(&self, scope: &str, turn: &str, at: i64, required: u64) -> Result<()> {
        let reset = self.quota(at)?["reset_at"].as_i64().unwrap_or(at);
        self.0.execute("INSERT INTO waits(scope,turn_id,at,reset_at,required) VALUES (?,?,?,?,?) ON CONFLICT(scope) DO UPDATE SET turn_id=excluded.turn_id,reset_at=excluded.reset_at,required=excluded.required,released=0",params![scope,turn,at,reset,required])?;
        Ok(())
    }
    pub fn ready(&self, scope: &str, at: i64) -> Result<bool> {
        use rusqlite::OptionalExtension;
        let wait: Option<(u64, bool)> = self
            .0
            .query_row(
                "SELECT required,released FROM waits WHERE scope=?",
                [scope],
                |r| Ok((r.get(0)?, r.get(1)?)),
            )
            .optional()?;
        let Some((required, released)) = wait else {
            return Ok(false);
        };
        let p = self.policy()?;
        let q = self.quota(at)?;
        Ok((p.auto_resume || released)
            && (q["active"] != true || q["remaining"].as_u64().unwrap_or(0) >= required))
    }
    pub fn clear_wait(&self, scope: &str) -> Result<()> {
        self.0.execute("DELETE FROM waits WHERE scope=?", [scope])?;
        Ok(())
    }
    pub fn waiting(&self, scope: &str) -> Result<Option<Value>> {
        use rusqlite::OptionalExtension;
        Ok(self
            .0
            .query_row(
                "SELECT at,reset_at,required FROM waits WHERE scope=?",
                [scope],
                |r| Ok(json!({"since":r.get::<_,i64>(0)?,"reset_at":r.get::<_,i64>(1)?,"required":r.get::<_,u64>(2)?})),
            )
            .optional()?)
    }
    pub fn tool(&self, scope: &str, turn: &str, event: &Value, at: i64) -> Result<()> {
        let Some(call) = event["toolCallId"].as_str() else {
            return Ok(());
        };
        let key = format!("{scope}:{turn}:{call}");
        if event["type"] == "tool_execution_start" {
            self.0.execute(
                "INSERT OR IGNORE INTO tools(id,scope,turn_id,name,at) VALUES (?,?,?,?,?)",
                params![
                    key,
                    scope,
                    turn,
                    event["toolName"].as_str().unwrap_or("tool"),
                    at
                ],
            )?;
        } else if event["type"] == "tool_execution_end" {
            let bytes = serde_json::to_vec(&event["result"]["content"])?.len() as u64;
            self.0.execute(
                "UPDATE tools SET end_at=?,bytes=?,failed=? WHERE id=?",
                params![at, bytes, event["isError"] == true, key],
            )?;
        }
        Ok(())
    }
    pub fn compaction(&self, scope: &str, turn: &str, event: &Value, at: i64) -> Result<()> {
        let id = event["id"]
            .as_str()
            .ok_or_else(|| anyhow!("Missing compaction ID"))?;
        uuid::Uuid::parse_str(id)?;
        let number = |key: &str| -> Result<Option<u64>> {
            if event[key].is_null() {
                return Ok(None);
            }
            let n = event[key]
                .as_u64()
                .ok_or_else(|| anyhow!("Invalid context size"))?;
            anyhow::ensure!(n <= 9_007_199_254_740_991, "Invalid context size");
            Ok(Some(n))
        };
        let before = number("tokens_before")?;
        let after = number("tokens_after")?;
        match event["action"].as_str().unwrap_or("") {
            "start" => {
                let reason = event["reason"].as_str().unwrap_or("");
                anyhow::ensure!(
                    ["manual", "threshold", "overflow"].contains(&reason),
                    "Invalid compaction reason"
                );
                use rusqlite::OptionalExtension;
                let owner: Option<(String, String)> = self
                    .0
                    .query_row(
                        "SELECT scope,turn_id FROM compactions WHERE id=?",
                        [id],
                        |r| Ok((r.get(0)?, r.get(1)?)),
                    )
                    .optional()?;
                if let Some(owner) = owner {
                    anyhow::ensure!(
                        owner == (scope.into(), turn.into()),
                        "Compaction belongs to another turn"
                    );
                    return Ok(());
                }
                self.interrupt_compactions(scope, "interrupted", at)?;
                self.0.execute("INSERT INTO compactions(id,scope,turn_id,at,reason,tokens_before) VALUES (?,?,?,?,?,?)",params![id,scope,turn,at,reason,before])?;
            }
            state @ ("complete" | "failed" | "cancelled") => {
                let entry = event["entry_id"].as_str();
                anyhow::ensure!(entry.is_none_or(|v| v.len() <= 128), "Invalid entry ID");
                let changed = self.0.execute("UPDATE compactions SET state=?,end_at=COALESCE(end_at,?),tokens_before=COALESCE(?,tokens_before),tokens_after=COALESCE(?,tokens_after),entry_id=COALESCE(?,entry_id) WHERE id=? AND scope=? AND turn_id=? AND (state='running' OR state=?)",params![state,at,before,after,entry,id,scope,turn,state])?;
                anyhow::ensure!(changed == 1, "Unknown or finished compaction");
            }
            _ => bail!("Invalid compaction event"),
        }
        Ok(())
    }
    pub fn interrupt_compactions(&self, scope: &str, state: &str, at: i64) -> Result<()> {
        self.0.execute(
            "UPDATE compactions SET state=?,end_at=? WHERE scope=? AND state='running'",
            params![state, at, scope],
        )?;
        Ok(())
    }
    pub fn validate_compaction(&self, scope: &str, turn: &str, id: &str) -> Result<()> {
        let valid: bool = self.0.query_row("SELECT EXISTS(SELECT 1 FROM compactions WHERE id=? AND scope=? AND turn_id=? AND state='running')",params![id,scope,turn],|r|r.get(0))?;
        anyhow::ensure!(valid, "Unknown or inactive compaction");
        Ok(())
    }
    pub fn tag_compaction(&self, request: &str, operation: &str) -> Result<()> {
        self.0.execute(
            "UPDATE requests SET compaction_id=? WHERE id=?",
            params![operation, request],
        )?;
        Ok(())
    }

    pub fn summary(&self, q: &QueryArgs, at: i64) -> Result<Value> {
        anyhow::ensure!(
            [7, 30, 90, 365].contains(&q.days),
            "Use 7, 30, 90 or 365 days"
        );
        anyhow::ensure!(
            q.scope.len() <= 128 && q.offset <= 1_000_000,
            "Invalid query"
        );
        let tz: Tz = q.timezone.parse()?;
        let today = Utc
            .timestamp_millis_opt(at)
            .single()
            .ok_or_else(|| anyhow!("Invalid time"))?
            .with_timezone(&tz)
            .date_naive();
        let first = today
            .checked_sub_days(Days::new(q.days as u64 - 1))
            .unwrap();
        let start = midnight(tz, first)?;
        let end = midnight(tz, today.succ_opt().unwrap())?;
        let width = if q.days == 365 { 7 } else { 1 };
        let mut buckets:Vec<Value>=(0..q.days).step_by(width).map(|i| {
            let a=first.checked_add_days(Days::new(i as u64)).unwrap();
            let b=a.checked_add_days(Days::new((width as u32-1).min(q.days-i-1) as u64)).unwrap();
            json!({"label":a.format("%m-%d").to_string(),"range":format!("{a} — {b}"),"input":0,"output":0,"cache":0,"unknown":0})
        }).collect();
        let aggregate = |from: i64, to: i64| -> Result<Totals> {
            Ok(self.0.query_row("SELECT COUNT(*),COALESCE(SUM(input),0),COALESCE(SUM(output),0),COALESCE(SUM(cache),0),COALESCE(SUM(CASE WHEN input IS NULL OR output IS NULL OR state='native_incomplete' THEN 1 ELSE 0 END),0) FROM requests WHERE (?='' OR scope=?) AND at>=? AND at<?",params![q.scope,q.scope,from,to],|r|Ok(Totals{requests:r.get(0)?,input:r.get(1)?,output:r.get(2)?,cache:r.get(3)?,unknown:r.get(4)?}))?)
        };
        let lifetime = aggregate(0, i64::MAX)?;
        let historical = aggregate(0, 1)?;
        let total = aggregate(start, end.min(at.saturating_add(1)))?;
        for (index, bucket) in buckets.iter_mut().enumerate() {
            let a = first
                .checked_add_days(Days::new((index * width) as u64))
                .unwrap();
            let b = a
                .checked_add_days(Days::new(width as u64))
                .unwrap()
                .min(today.succ_opt().unwrap());
            let sums = aggregate(midnight(tz, a)?, midnight(tz, b)?.min(at.saturating_add(1)))?;
            bucket["input"] = json!(sums.input);
            bucket["output"] = json!(sums.output);
            bucket["cache"] = json!(sums.cache);
            bucket["unknown"] = json!(sums.unknown);
        }
        let mut requests = vec![];
        let mut tools = vec![];
        if !q.scope.is_empty() {
            let mut stmt=self.0.prepare("SELECT id,turn_id,at,model,input,output,cache,state,compaction_id FROM requests WHERE scope=? ORDER BY at DESC,id DESC LIMIT 20 OFFSET ?")?;
            for row in stmt.query_map(params![q.scope,q.offset],|r|Ok(json!({"id":r.get::<_,String>(0)?,"turn":r.get::<_,String>(1)?,"at":r.get::<_,i64>(2)?,"label":if r.get::<_,i64>(2)?==0 {"历史 · 时间未知".into()}else{Utc.timestamp_millis_opt(r.get::<_,i64>(2)?).single().unwrap().with_timezone(&tz).format("%Y-%m-%d %H:%M").to_string()},"model":r.get::<_,String>(3)?,"input":r.get::<_,Option<u64>>(4)?,"output":r.get::<_,Option<u64>>(5)?,"cache":r.get::<_,u64>(6)?,"state":r.get::<_,String>(7)?,"purpose":if r.get::<_,Option<String>>(8)?.is_some(){"compaction"}else{"conversation"}})))? {requests.push(row?);}
            let mut labels = std::collections::HashMap::new();
            let mut rounds=self.0.prepare("SELECT turn_id,DENSE_RANK() OVER (ORDER BY MIN(at),turn_id) FROM requests WHERE scope=? AND at>0 GROUP BY turn_id")?;
            for row in rounds.query_map([&q.scope], |r| {
                Ok((r.get::<_, String>(0)?, r.get::<_, u64>(1)?))
            })? {
                let (turn, index) = row?;
                labels.insert(turn, format!("第 {index} 轮"));
            }
            for request in &mut requests {
                request["turn_label"] = json!(
                    labels
                        .get(request["turn"].as_str().unwrap_or(""))
                        .cloned()
                        .unwrap_or("历史未分类".into())
                );
            }
            let mut stmt=self.0.prepare("SELECT name,COUNT(*),SUM(failed),COALESCE(SUM(bytes),0),COALESCE(SUM(end_at-at),0),SUM(CASE WHEN end_at IS NULL THEN 1 ELSE 0 END) FROM tools WHERE scope=? GROUP BY name ORDER BY COALESCE(SUM(bytes),0) DESC,name")?;
            for row in stmt.query_map([&q.scope],|r|Ok(json!({"name":r.get::<_,String>(0)?,"calls":r.get::<_,u64>(1)?,"failed":r.get::<_,u64>(2)?,"bytes":r.get::<_,u64>(3)?,"duration_ms":r.get::<_,u64>(4)?,"unfinished":r.get::<_,u64>(5)?})))? {tools.push(row?);}
        }
        let mut compactions = vec![];
        let compaction_count: u64 = self.0.query_row(
            "SELECT COUNT(*) FROM compactions WHERE scope=?",
            [&q.scope],
            |r| r.get(0),
        )?;
        let compaction_totals: Totals = self.0.query_row("SELECT COUNT(*),COALESCE(SUM(input),0),COALESCE(SUM(output),0),COALESCE(SUM(cache),0),COALESCE(SUM(CASE WHEN input IS NULL OR output IS NULL OR state='native_incomplete' THEN 1 ELSE 0 END),0) FROM requests WHERE scope=? AND compaction_id IS NOT NULL",[&q.scope],|r|Ok(Totals{requests:r.get(0)?,input:r.get(1)?,output:r.get(2)?,cache:r.get(3)?,unknown:r.get(4)?}))?;
        if !q.scope.is_empty() {
            let mut stmt = self.0.prepare("SELECT c.id,c.at,c.reason,c.state,c.tokens_before,c.tokens_after,COUNT(r.id),COALESCE(SUM(r.input),0),COALESCE(SUM(r.output),0),COALESCE(SUM(r.cache),0),COALESCE(SUM(CASE WHEN r.id IS NOT NULL AND (r.input IS NULL OR r.output IS NULL OR r.state='native_incomplete') THEN 1 ELSE 0 END),0) FROM compactions c LEFT JOIN requests r ON r.compaction_id=c.id WHERE c.scope=? GROUP BY c.id ORDER BY c.at DESC,c.id DESC LIMIT 20 OFFSET ?")?;
            for row in stmt.query_map(params![q.scope,q.offset],|r|Ok(json!({"id":r.get::<_,String>(0)?,"label":Utc.timestamp_millis_opt(r.get::<_,i64>(1)?).single().unwrap().with_timezone(&tz).format("%Y-%m-%d %H:%M").to_string(),"reason":r.get::<_,String>(2)?,"state":r.get::<_,String>(3)?,"tokens_before":r.get::<_,Option<u64>>(4)?,"tokens_after":r.get::<_,Option<u64>>(5)?,"requests":r.get::<_,u64>(6)?,"input":r.get::<_,u64>(7)?,"output":r.get::<_,u64>(8)?,"cache":r.get::<_,u64>(9)?,"unknown":r.get::<_,u64>(10)?})))? { compactions.push(row?); }
        }
        let native_turn_usage: bool = self.0.query_row(
            "SELECT EXISTS(SELECT 1 FROM requests WHERE (?='' OR scope=?) AND state LIKE 'native_%')",
            params![q.scope,q.scope], |row| row.get(0))?;
        Ok(
            json!({"native_turn_usage":native_turn_usage,"days":q.days,"timezone":q.timezone,"scope":q.scope,"bucket_days":width,"total":total,"lifetime":lifetime,"historical":historical,"buckets":buckets,"requests":requests,"tools":tools,"offset":q.offset,"next_offset":if q.offset+20<lifetime.requests as usize {Some(q.offset+20)} else {None},"quota":self.quota(at)?,"waiting":self.waiting(&q.scope)?,"compactions":compactions,"compaction_total":compaction_totals,"compaction_count":compaction_count,"compaction_next_offset":if q.offset+20<compaction_count as usize {Some(q.offset+20)} else {None},"capabilities":{"compaction":true,"provider_quota":false}}),
        )
    }
}
pub async fn summary(
    State(s): State<Service>,
    h: HeaderMap,
    Query(q): Query<QueryArgs>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    Ok(Json(s.usage.lock().unwrap().summary(&q, now()).map_err(
        |e| error(StatusCode::BAD_REQUEST, "usage_query", &e.to_string()),
    )?))
}
pub async fn policy(
    State(s): State<Service>,
    h: HeaderMap,
    Json(p): Json<Policy>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    s.usage
        .lock()
        .unwrap()
        .set_policy(&p)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "usage_policy", &e.to_string()))?;
    Ok(Json(json!({"saved":true})))
}

#[derive(Deserialize)]
pub struct Resume {
    scope: String,
}
pub async fn resume(
    State(s): State<Service>,
    h: HeaderMap,
    Json(r): Json<Resume>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    s.usage
        .lock()
        .unwrap()
        .0
        .execute("UPDATE waits SET released=1 WHERE scope=?", [&r.scope])
        .map(|_| ())
        .map_err(|e| internal(e.into()))?;
    Ok(Json(json!({"resumed":true})))
}

/// Only an explicitly checkpointed, fully stopped Pi turn can resume automatically.
pub fn paused(s: &Service) -> Result<Option<SendMessage>> {
    Ok(s.inner
        .lock()
        .unwrap()
        .store
        .get("pi.paused")?
        .filter(|v| !v.is_empty())
        .map(|v| serde_json::from_str(&v))
        .transpose()?)
}
pub fn resume_main(s: &Service) -> Result<()> {
    if s.inner.lock().unwrap().turn.is_some() {
        return Ok(());
    }
    let Some(req) = paused(s)? else { return Ok(()) };
    if !s.usage.lock().unwrap().ready("main", now())? {
        return Ok(());
    }
    let mut inner = s.inner.lock().unwrap();
    if inner.turn.is_some() || inner.store.get("pi.paused")?.is_none_or(|v| v.is_empty()) {
        return Ok(());
    }
    let message = inner
        .store
        .duplicate(&req.request_id)?
        .ok_or_else(|| anyhow!("Paused message missing"))?;
    inner.turn = Some(TurnState {
        user_message_id: message.id,
        cancelling: false,
    });
    inner.model_requests = 0;
    s.commands.send(req)?;
    Ok(())
}

pub async fn cancel_wait(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let mut inner = s.inner.lock().unwrap();
    if inner.turn.is_some() {
        return Err(error(
            StatusCode::CONFLICT,
            "running",
            "请在对话中停止正在执行的轮次",
        ));
    }
    if let Some(raw) = inner
        .store
        .get("pi.paused")
        .map_err(internal)?
        .filter(|v| !v.is_empty())
    {
        let req: SendMessage = serde_json::from_str(&raw).map_err(|e| internal(e.into()))?;
        if let Some(mut user) = inner.store.duplicate(&req.request_id).map_err(internal)? {
            user.status = MessageStatus::Cancelled;
            inner.store.save(user).map_err(internal)?;
        }
        inner.store.set("pi.paused", "").map_err(internal)?;
        inner.engine.detail = None;
        s.usage
            .lock()
            .unwrap()
            .clear_wait("main")
            .map_err(internal)?;
    }
    Ok(Json(json!({"cancelled":true})))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn ledger() -> Ledger {
        Ledger::open(std::path::Path::new(":memory:")).unwrap()
    }
    #[test]
    fn native_cumulative_usage_is_atomic_and_requires_a_known_baseline() {
        let l = ledger();
        l.0.execute(
            "INSERT INTO requests(id,scope,turn_id,at,model) VALUES ('r','main','turn',0,'mock')",
            [],
        )
        .unwrap();
        let totals = json!({"inputTokens":20,"outputTokens":5,"cachedInputTokens":4});
        assert!(l.native_usage("r", "thread", &totals).is_err());
        l.initialize_native_usage("thread").unwrap();
        l.native_usage("r", "thread", &totals).unwrap();
        l.native_usage("r", "thread", &totals).unwrap();
        let newer = json!({"inputTokens":40,"outputTokens":10,"cachedInputTokens":8});
        assert!(l.native_usage("missing", "thread", &newer).is_err());
        l.native_usage("r", "thread", &newer).unwrap();
        assert!(l.native_usage("r", "thread", &totals).is_err());
        assert!(
            l.native_usage("r", "thread", &json!({"inputTokens":50,"outputTokens":15}))
                .is_err()
        );
        let usage: (u64, u64, u64) =
            l.0.query_row(
                "SELECT input,output,cache FROM requests WHERE id='r'",
                [],
                |r| Ok((r.get(0)?, r.get(1)?, r.get(2)?)),
            )
            .unwrap();
        assert_eq!(usage, (40, 10, 8));
        l.native_incomplete("r").unwrap();
        let summary = l.summary(&query("main", 7), now()).unwrap();
        assert_eq!(summary["lifetime"]["unknown"], 1);
        assert_eq!(summary["lifetime"]["input"], 40);
        assert_eq!(summary["native_turn_usage"], true);
    }

    #[test]
    fn claude_usage_counts_cache_once_and_preserves_incomplete_totals() {
        let l = ledger();
        l.0.execute("INSERT INTO requests(id,scope,turn_id,at,model,state) VALUES ('c','main','turn',0,'mock','native_pending')", []).unwrap();
        let usage = json!({"input_tokens":20,"output_tokens":5,"cache_read_input_tokens":10,"cache_creation_input_tokens":3});
        assert!(l.settle_claude("missing", &usage).is_err());
        l.settle_claude("c", &usage).unwrap();
        l.settle_claude("c", &usage).unwrap();
        assert!(l.settle_claude("c", &json!({"input_tokens":1})).is_err());
        assert!(l.settle_claude("c", &json!({"input_tokens":u64::MAX,"output_tokens":5,"cache_read_input_tokens":1,"cache_creation_input_tokens":0})).is_err());
        l.native_incomplete("c").unwrap();
        let summary = l.summary(&query("main", 7), now()).unwrap();
        assert_eq!(summary["lifetime"]["input"], 33);
        assert_eq!(summary["lifetime"]["output"], 5);
        assert_eq!(summary["lifetime"]["cache"], 10);
        assert_eq!(summary["lifetime"]["unknown"], 1);
    }

    fn at(s: &str) -> i64 {
        chrono::DateTime::parse_from_rfc3339(s)
            .unwrap()
            .timestamp_millis()
    }
    fn query(scope: &str, days: u32) -> QueryArgs {
        QueryArgs {
            scope: scope.into(),
            days,
            timezone: "Asia/Shanghai".into(),
            offset: 0,
        }
    }
    #[test]
    fn compaction_usage_is_a_subset_and_lifecycle_is_scoped_and_durable() {
        let dir = std::env::temp_dir().join(format!("pot-compaction-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("usage.sqlite");
        let l = Ledger::open(&path).unwrap();
        let at = now();
        let op = id();
        let start = json!({"id":op,"action":"start","reason":"threshold","tokens_before":28000});
        l.compaction("task", "turn", &start, at).unwrap();
        l.compaction("task", "turn", &start, at).unwrap();
        assert!(l.compaction("other", "turn", &start, at).is_err());
        assert!(l.validate_compaction("other", "turn", &op).is_err());
        for (request, usage) in [
            (
                "summary-a",
                json!({"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":30}),
            ),
            (
                "summary-b",
                json!({"prompt_tokens":50,"completion_tokens":10}),
            ),
        ] {
            l.reserve(request, "task", "turn", at, "mock", 200, 100)
                .unwrap();
            l.tag_compaction(request, &op).unwrap();
            l.settle(request, &usage).unwrap();
            l.settle(request, &usage).unwrap();
        }
        l.reserve("conversation", "task", "turn", at, "mock", 100, 50)
            .unwrap();
        l.settle(
            "conversation",
            &json!({"prompt_tokens":10,"completion_tokens":5}),
        )
        .unwrap();
        let end = json!({"id":op,"action":"complete","tokens_after":4000,"usage":{"input":999999}});
        l.compaction("task", "turn", &end, at + 1).unwrap();
        l.compaction("task", "turn", &end, at + 1).unwrap();
        assert!(l.validate_compaction("task", "turn", &op).is_err());
        let unfinished = id();
        l.compaction(
            "task",
            "next",
            &json!({"id":unfinished,"action":"start","reason":"overflow"}),
            at + 2,
        )
        .unwrap();
        drop(l);
        let l = Ledger::open(&path).unwrap();
        let d = l.summary(&query("task", 7), at + 3).unwrap();
        assert_eq!(d["lifetime"]["input"], 160);
        assert_eq!(d["lifetime"]["output"], 35);
        assert_eq!(d["compaction_total"]["input"], 150);
        assert_eq!(d["compaction_total"]["output"], 30);
        assert_eq!(d["compaction_total"]["cache"], 30);
        assert_eq!(d["compaction_count"], 2);
        assert_eq!(d["compactions"][0]["state"], "interrupted");
        assert_eq!(d["compactions"][1]["tokens_after"], 4000);
        assert!(d["capabilities"].get("skills").is_none());
    }

    #[test]
    fn old_usage_schema_migrates_without_losing_requests() {
        let dir = std::env::temp_dir().join(format!("pot-usage-migrate-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("usage.sqlite");
        let c = Connection::open(&path).unwrap();
        c.execute_batch("CREATE TABLE requests(id TEXT PRIMARY KEY,scope TEXT NOT NULL,turn_id TEXT NOT NULL,at INTEGER NOT NULL,model TEXT NOT NULL,input INTEGER,output INTEGER,cache INTEGER NOT NULL DEFAULT 0,reserved INTEGER NOT NULL DEFAULT 0,state TEXT NOT NULL DEFAULT 'pending'); INSERT INTO requests(id,scope,turn_id,at,model,input,output,state) VALUES ('old','main','turn',0,'mock',10,5,'reported');").unwrap();
        drop(c);
        let l = Ledger::open(&path).unwrap();
        let d = l.summary(&query("main", 7), now()).unwrap();
        assert_eq!(d["lifetime"]["input"], 10);
        assert_eq!(d["requests"][0]["purpose"], "conversation");
        assert_eq!(d["compaction_count"], 0);
    }

    #[test]
    fn usage_is_idempotent_scoped_and_excludes_cache_from_total() {
        let l = ledger();
        let t = at("2026-10-04T01:00:00Z");
        for (id, scope) in [("a", "main"), ("b", "task-1")] {
            l.reserve(id, scope, "turn", t, "mock", 100, 50).unwrap();
            for _ in 0..2 {
                l.settle(id,&json!({"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":60})).unwrap();
            }
        }
        let all = l.summary(&query("", 7), t).unwrap();
        assert_eq!(
            all["total"],
            json!({"input":200,"output":40,"cache":120,"requests":2,"unknown":0})
        );
        let task = l.summary(&query("task-1", 7), t).unwrap();
        assert_eq!(task["lifetime"]["requests"], 1);
        assert_eq!(task["requests"][0]["turn"], "turn");
        assert_eq!(all["buckets"].as_array().unwrap().len(), 7);
        assert!(all["requests"].as_array().unwrap().is_empty());
        assert!(all["quota"]["active"] == false);
        assert_eq!(
            l.summary(&query("", 365), t).unwrap()["buckets"]
                .as_array()
                .unwrap()
                .len(),
            53
        );
    }
    #[test]
    fn timezone_ranges_pagination_and_undated_history() {
        let l = ledger();
        let s = Store::open(std::path::Path::new(":memory:")).unwrap();
        s.set(
            "pi.usage",
            &json!([{"prompt_tokens":3,"completion_tokens":2}]).to_string(),
        )
        .unwrap();
        l.import_legacy("task", &s).unwrap();
        l.import_legacy("task", &s).unwrap();
        let t = at("2026-10-04T01:00:00Z");
        for i in 0..21 {
            l.reserve(&format!("r{i}"), "task", "turn", t, "mock", 10, 10)
                .unwrap();
            l.settle(
                &format!("r{i}"),
                &json!({"prompt_tokens":10,"completion_tokens":5}),
            )
            .unwrap();
        }
        let d = l.summary(&query("task", 7), t).unwrap();
        assert_eq!(d["historical"]["requests"], 1);
        assert_eq!(d["lifetime"]["requests"], 22);
        assert_eq!(d["total"]["requests"], 21);
        assert_eq!(d["next_offset"], 20);
        let mut q = query("task", 7);
        q.offset = 20;
        assert_eq!(
            l.summary(&q, t).unwrap()["requests"]
                .as_array()
                .unwrap()
                .len(),
            2
        );
        q.days = 1;
        assert!(l.summary(&q, t).is_err());
        q.days = 7;
        q.timezone = "invalid".into();
        assert!(l.summary(&q, t).is_err());
        let boundary = at("2026-09-27T16:00:00Z");
        l.reserve("edge", "main", "t", boundary, "m", 10, 10)
            .unwrap();
        l.settle("edge", &json!({"prompt_tokens":1,"completion_tokens":1}))
            .unwrap();
        assert_eq!(
            l.summary(&query("main", 7), t).unwrap()["total"]["requests"],
            1
        );
    }
    #[test]
    fn quotas_reserve_across_lanes_reset_and_require_manual_resume() {
        let l = ledger();
        let t = at("2026-10-04T01:00:00Z");
        let mut p = Policy {
            period: "day".into(),
            limit: 100,
            timezone: "Asia/Shanghai".into(),
            auto_resume: true,
        };
        l.set_policy(&p).unwrap();
        assert_eq!(
            l.reserve("a", "main", "turn", t, "m", 30, 60).unwrap(),
            Some(60)
        );
        assert_eq!(
            l.reserve("b", "task", "turn", t, "m", 20, 40).unwrap(),
            None
        );
        l.wait("task", "turn", t, 21).unwrap();
        assert!(!l.ready("task", t).unwrap());
        l.settle("a", &json!({"prompt_tokens":30,"completion_tokens":10}))
            .unwrap();
        assert!(l.ready("task", t).unwrap());
        assert_eq!(
            l.reserve("b", "task", "turn", t, "m", 20, 80).unwrap(),
            Some(40)
        );
        assert!(l.waiting("task").unwrap().is_none());
        l.wait("other", "turn", t, 10).unwrap();
        p.auto_resume = false;
        l.set_policy(&p).unwrap();
        let next = at("2026-10-04T16:00:00Z");
        assert_eq!(l.quota(next).unwrap()["remaining"], 100);
        assert!(!l.ready("other", next).unwrap());
        l.0.execute("UPDATE waits SET released=1 WHERE scope='other'", [])
            .unwrap();
        assert!(l.ready("other", next).unwrap());
        assert_eq!(
            l.summary(&query("", 7), next).unwrap()["lifetime"]["requests"],
            2
        );
    }
    #[test]
    fn quota_windows_follow_local_dst_and_monday() {
        let mut p = Policy {
            period: "day".into(),
            limit: 100,
            timezone: "America/New_York".into(),
            auto_resume: true,
        };
        let (a, b) = p.window(at("2026-03-08T15:00:00Z")).unwrap();
        assert_eq!(b - a, 23 * 3600 * 1000);
        p.period = "week".into();
        let (a, b) = p.window(at("2026-03-08T15:00:00Z")).unwrap();
        assert_eq!(a, at("2026-03-02T05:00:00Z"));
        assert_eq!(b, at("2026-03-09T04:00:00Z"));
    }
    #[test]
    fn unknown_requests_waits_and_policy_survive_reopen() {
        let path = std::env::temp_dir().join(format!("pot-usage-{}.sqlite", id()));
        let t = now();
        {
            let l = Ledger::open(&path).unwrap();
            l.set_policy(&Policy {
                period: "day".into(),
                limit: 100,
                ..Policy::default()
            })
            .unwrap();
            l.reserve("r", "main", "t", t, "m", 50, 50).unwrap();
            l.wait("main", "t", t, 20).unwrap();
        }
        {
            let l = Ledger::open(&path).unwrap();
            assert_eq!(l.quota(t).unwrap()["reserved"], 100);
            assert!(l.waiting("main").unwrap().is_some());
            assert_eq!(
                l.summary(&query("main", 7), t).unwrap()["lifetime"]["unknown"],
                1
            );
        }
        std::fs::remove_file(path).unwrap();
    }
    #[test]
    fn tool_receipts_do_not_duplicate_and_are_isolated_by_turn() {
        let l = ledger();
        for turn in ["one", "two"] {
            for _ in 0..2 {
                l.tool(
                    "task",
                    turn,
                    &json!({"type":"tool_execution_start","toolCallId":"same","toolName":"bash"}),
                    10,
                )
                .unwrap();
                l.tool("task",turn,&json!({"type":"tool_execution_end","toolCallId":"same","result":{"content":[{"text":"abc"}]},"isError":false}),20).unwrap();
            }
        }
        let rows = l.summary(&query("task", 7), now()).unwrap();
        assert_eq!(rows["tools"][0]["calls"], 2);
        assert_eq!(rows["tools"][0]["duration_ms"], 20);
    }
    #[tokio::test]
    async fn endpoints_require_root_authority_and_validate_settings() {
        let s = crate::tests::service();
        assert!(
            summary(State(s.clone()), HeaderMap::new(), Query(query("", 7)))
                .await
                .is_err()
        );
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        assert!(
            policy(
                State(s.clone()),
                h.clone(),
                Json(Policy {
                    limit: 0,
                    ..Policy::default()
                })
            )
            .await
            .is_err()
        );
        assert!(summary(State(s), h, Query(query("", 7))).await.is_ok());
    }
}

pub fn settle_cancelled_pause(s: &Service) -> Result<()> {
    let mut inner = s.inner.lock().unwrap();
    if !inner.turn.as_ref().is_some_and(|t| t.cancelling) {
        return Ok(());
    }
    if let Some(raw) = inner.store.get("pi.paused")?.filter(|s| !s.is_empty()) {
        let req: SendMessage = serde_json::from_str(&raw)?;
        if let Some(mut m) = inner.store.duplicate(&req.request_id)? {
            m.status = MessageStatus::Cancelled;
            inner.store.save(m)?;
        }
        inner.store.set("pi.paused", "")?;
        inner.engine.detail = None;
        s.usage.lock().unwrap().clear_wait(&s.usage_scope)?;
    }
    Ok(())
}

impl Ledger {
    /// CLI subscription usage is post-hoc. A pending turn has unknown usage, not zero.
    pub fn begin_native(
        &self,
        id: &str,
        scope: &str,
        turn: &str,
        spec: &agent::ResolvedSpec,
        epoch: u64,
    ) -> Result<()> {
        let quota = self.quota(now())?;
        anyhow::ensure!(
            quota["active"] != true || quota["remaining"].as_u64().unwrap_or(0) > 0,
            "Pot allowance is exhausted; native CLI turns require manual continuation after it resets"
        );
        let tx = self.0.unchecked_transaction()?;
        self.0.execute("INSERT INTO requests(id,scope,turn_id,at,model,state) VALUES (?,?,?,?,?,'native_pending')", params![id,scope,turn,now(),spec.model])?;
        self.route(id, &spec.provider, epoch, &spec.spec_hash)?;
        tx.commit()?;
        Ok(())
    }
    pub fn settle_claude(&self, request: &str, usage: &Value) -> Result<()> {
        let input = usage["input_tokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing Claude input usage"))?;
        let output = usage["output_tokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing Claude output usage"))?;
        let cache = usage["cache_read_input_tokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing Claude cache usage"))?;
        let created = usage["cache_creation_input_tokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing Claude cache creation usage"))?;
        let input = input
            .checked_add(cache)
            .and_then(|v| v.checked_add(created))
            .ok_or_else(|| anyhow!("Claude usage overflow"))?;
        let tx = self.0.unchecked_transaction()?;
        self.settle(request,&json!({"prompt_tokens":input,"completion_tokens":output,"prompt_cache_hit_tokens":cache}))?;
        anyhow::ensure!(
            self.0.execute(
                "UPDATE requests SET state='native_reported' WHERE id=?",
                [request],
            )? == 1,
            "Missing native usage request"
        );
        tx.commit()?;
        Ok(())
    }

    pub fn native_incomplete(&self, request: &str) -> Result<()> {
        self.0.execute(
            "UPDATE requests SET state='native_incomplete' WHERE id=?",
            [request],
        )?;
        Ok(())
    }

    pub fn initialize_native_usage(&self, native: &str) -> Result<()> {
        self.0.execute_batch("CREATE TABLE IF NOT EXISTS native_usage(native TEXT PRIMARY KEY,input INTEGER NOT NULL,output INTEGER NOT NULL,cache INTEGER NOT NULL)")?;
        self.0.execute(
            "INSERT OR IGNORE INTO native_usage VALUES (?,0,0,0)",
            [native],
        )?;
        Ok(())
    }
    /// Persist the cumulative baseline and per-turn difference atomically. Duplicate
    /// notifications cannot add usage twice, including after Runtime restart.
    pub fn native_usage(&self, request: &str, native: &str, totals: &Value) -> Result<()> {
        self.0.execute_batch("CREATE TABLE IF NOT EXISTS native_usage(native TEXT PRIMARY KEY,input INTEGER NOT NULL,output INTEGER NOT NULL,cache INTEGER NOT NULL)")?;
        let input = totals["inputTokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing native input usage"))?;
        let output = totals["outputTokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing native output usage"))?;
        let cache = totals["cachedInputTokens"]
            .as_u64()
            .ok_or_else(|| anyhow!("Missing native cached usage"))?;
        anyhow::ensure!(cache <= input, "Invalid native cached usage");
        anyhow::ensure!(
            input <= i64::MAX as u64 && output <= i64::MAX as u64,
            "Invalid native usage"
        );
        let tx = self.0.unchecked_transaction()?;
        let previous = self
            .0
            .query_row(
                "SELECT input,output,cache FROM native_usage WHERE native=?",
                [native],
                |r| {
                    Ok((
                        r.get::<_, u64>(0)?,
                        r.get::<_, u64>(1)?,
                        r.get::<_, u64>(2)?,
                    ))
                },
            )
            .map_err(|_| anyhow!("Native usage baseline unavailable; historical usage cannot be attributed to this turn"))?;
        anyhow::ensure!(
            input >= previous.0 && output >= previous.1 && cache >= previous.2,
            "Native usage counter reset; usage precision unavailable"
        );
        let changed = self.0.execute("UPDATE requests SET input=COALESCE(input,0)+?,output=COALESCE(output,0)+?,cache=cache+?,state='native_reported' WHERE id=?", params![input-previous.0,output-previous.1,cache-previous.2,request])?;
        anyhow::ensure!(changed == 1, "Native usage request missing");
        self.0.execute("INSERT INTO native_usage VALUES (?,?,?,?) ON CONFLICT(native) DO UPDATE SET input=excluded.input,output=excluded.output,cache=excluded.cache",params![native,input,output,cache])?;
        tx.commit()?;
        Ok(())
    }
}
