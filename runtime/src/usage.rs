//! Runtime allowance and recovery use cases. Persistence lives in pot-storage.
use super::*;
pub use pot_storage::usage::*;
pub mod providers;
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

/// Only an explicitly checkpointed, fully stopped Pot turn can resume automatically.
pub fn paused(s: &Service) -> Result<Option<SendMessage>> {
    Ok(s.inner
        .lock()
        .unwrap()
        .store
        .get("execution.paused")?
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
    if inner.turn.is_some()
        || inner
            .store
            .get("execution.paused")?
            .is_none_or(|v| v.is_empty())
    {
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
        .get("execution.paused")
        .map_err(internal)?
        .filter(|v| !v.is_empty())
    {
        let req: SendMessage = serde_json::from_str(&raw).map_err(|e| internal(e.into()))?;
        if let Some(mut user) = inner.store.duplicate(&req.request_id).map_err(internal)? {
            user.status = MessageStatus::Cancelled;
            inner.store.save(user).map_err(internal)?;
        }
        inner.store.set("execution.paused", "").map_err(internal)?;
        inner.agent.detail = None;
        s.usage
            .lock()
            .unwrap()
            .clear_wait("main")
            .map_err(internal)?;
    }
    Ok(Json(json!({"cancelled":true})))
}

pub fn settle_cancelled_pause(s: &Service) -> Result<()> {
    let mut inner = s.inner.lock().unwrap();
    if !inner.turn.as_ref().is_some_and(|t| t.cancelling) {
        return Ok(());
    }
    if let Some(raw) = inner
        .store
        .get("execution.paused")?
        .filter(|s| !s.is_empty())
    {
        let req: SendMessage = serde_json::from_str(&raw)?;
        if let Some(mut m) = inner.store.duplicate(&req.request_id)? {
            m.status = MessageStatus::Cancelled;
            inner.store.save(m)?;
        }
        inner.store.set("execution.paused", "")?;
        inner.agent.detail = None;
        s.usage.lock().unwrap().clear_wait(&s.usage_scope)?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    fn ledger() -> Ledger {
        Ledger::open(std::path::Path::new(":memory:")).unwrap()
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
        let path = dir.join("usage-v2.sqlite");
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

    fn attributed_spec(model: &str, profile_id: &str) -> agent::ResolvedSpec {
        let mut profile = agent::Profile::api(&execution::Config {
            api_key: "unused".into(),
            model: model.into(),
            max_tokens: 100,
            request_limit: 100,
            max_requests_per_turn: 10,
            max_parallel_tasks: 2,
        });
        profile.id = profile_id.into();
        profile.auth_ref = "providers.json:deepseek".into();
        profile.resolve().unwrap()
    }
    #[test]
    fn agent_model_usage_keeps_snapshot_scope_window_and_unknown_counts() {
        let l = ledger();
        let t = at("2026-10-04T01:00:00Z");
        for (key, scope, model, stamp) in [
            ("p", "main", "shared", t),
            ("c", "main", "shared", t),
            ("a", "task-1", "worker-model", t),
            ("older", "main", "old-model", t - 10 * 86400000),
        ] {
            let spec = attributed_spec(model, key);
            l.reserve(key, scope, "turn", stamp, model, 100, 20)
                .unwrap();
            l.route(key, &spec, 2).unwrap();
            for _ in 0..2 {
                l.settle(key,&json!({"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":60})).unwrap();
            }
        }
        let pending = attributed_spec("shared", "pending-profile");
        l.reserve("pending", "main", "other-turn", t, "shared", 100, 20)
            .unwrap();
        l.route("pending", &pending, 3).unwrap();
        let all = l.summary(&query("", 7), t).unwrap();
        let groups = all["agent_models"].as_array().unwrap();
        assert_eq!(groups.len(), 2);
        let group = groups.iter().find(|v| v["model"] == "shared").unwrap();
        assert_eq!(group["requests"], 3);
        assert_eq!(group["unknown"], 1);
        assert_eq!(group["input"], 200);
        for field in ["input", "output", "cache", "requests", "unknown"] {
            assert_eq!(
                groups
                    .iter()
                    .map(|v| v[field].as_u64().unwrap())
                    .sum::<u64>(),
                all["total"][field].as_u64().unwrap()
            );
        }
        let main = l.summary(&query("main", 7), t).unwrap();
        assert!(
            main["agent_models"]
                .as_array()
                .unwrap()
                .iter()
                .any(|v| v["agent"] == "pot" && v["model"] == "old-model")
        );
        assert!(
            !main["agent_models"]
                .as_array()
                .unwrap()
                .iter()
                .any(|v| v["model"] == "worker-model")
        );
        let row = main["requests"]
            .as_array()
            .unwrap()
            .iter()
            .find(|r| r["id"] == "pending")
            .unwrap();
        assert_eq!(row["agent"], "pot");
        assert_eq!(row["profile_id"], "pending-profile");
        assert_eq!(row["generation"], 3);
    }

    #[test]
    fn timezone_ranges_and_pagination() {
        let l = ledger();
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
        assert_eq!(d["lifetime"]["requests"], 21);
        assert_eq!(d["total"]["requests"], 21);
        assert_eq!(d["next_offset"], 20);
        let mut q = query("task", 7);
        q.offset = 20;
        assert_eq!(
            l.summary(&q, t).unwrap()["requests"]
                .as_array()
                .unwrap()
                .len(),
            1
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
