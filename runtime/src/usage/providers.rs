//! Provider allowances supplement, never replace, the global allowance.
use super::*;
use rusqlite::OptionalExtension;

impl Ledger {
    pub fn provider_policy(&self, provider: &str) -> Result<(u64, Policy)> {
        let value: Option<String> = self
            .0
            .query_row(
                "SELECT value FROM settings WHERE key=?",
                [format!("provider:{provider}")],
                |r| r.get(0),
            )
            .optional()?;
        Ok(value
            .map(|s| serde_json::from_str(&s))
            .transpose()?
            .unwrap_or_default())
    }
    pub fn provider_quota(&self, provider: &str, at: i64) -> Result<Value> {
        let (revision, p) = self.provider_policy(provider)?;
        if p.period == "none" {
            return Ok(json!({"provider":provider,"revision":revision,"policy":p,"active":false}));
        }
        let (start, end) = p.window(at)?;
        let (used,reserved):(u64,u64) = self.0.query_row("SELECT COALESCE(SUM(COALESCE(r.input,0)+COALESCE(r.output,0)),0),COALESCE(SUM(r.reserved),0) FROM requests r JOIN request_routes t ON t.request_id=r.id WHERE t.provider=? AND r.at>=? AND r.at<?",params![provider,start,end],|r|Ok((r.get(0)?,r.get(1)?)))?;
        Ok(
            json!({"provider":provider,"revision":revision,"policy":p,"active":true,"used":used,"reserved":reserved,"remaining":p.limit.saturating_sub(used.saturating_add(reserved)),"reset_at":end}),
        )
    }
    pub fn set_provider_policy(
        &self,
        provider: &str,
        revision: u64,
        policy: &Policy,
    ) -> Result<()> {
        policy.validate()?;
        anyhow::ensure!(
            self.provider_policy(provider)?.0 == revision,
            "Provider allowance changed; refresh before saving"
        );
        let next = revision
            .checked_add(1)
            .ok_or_else(|| anyhow!("Revision overflow"))?;
        self.0.execute(
            "INSERT INTO settings VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
            params![
                format!("provider:{provider}"),
                serde_json::to_string(&(next, policy))?
            ],
        )?;
        Ok(())
    }
    pub fn reserve_routed(
        &self,
        id: &str,
        scope: &str,
        turn: &str,
        at: i64,
        spec: &agent::ResolvedSpec,
        epoch: u64,
        input: u64,
        output: u64,
    ) -> Result<Option<u64>> {
        let q = self.provider_quota(&spec.provider, at)?;
        let output = if q["active"] == true {
            let available = q["remaining"].as_u64().unwrap_or(0).saturating_sub(input);
            if available == 0 {
                return Ok(None);
            }
            output.min(available)
        } else {
            output
        };
        // Reservation and route must commit together: another lane cannot miss this provider claim.
        self.0.execute_batch("SAVEPOINT routed_reservation")?;
        let result = (|| -> Result<Option<u64>> {
            let value = self.reserve(id, scope, turn, at, &spec.model, input, output)?;
            if value.is_some() {
                self.route(id, &spec.provider, epoch, &spec.spec_hash)?;
            }
            Ok(value)
        })();
        match result {
            Ok(value) => {
                self.0.execute_batch("RELEASE routed_reservation")?;
                Ok(value)
            }
            Err(e) => {
                let _ = self
                    .0
                    .execute_batch("ROLLBACK TO routed_reservation; RELEASE routed_reservation");
                Err(e)
            }
        }
    }
    pub fn wait_routed(
        &self,
        scope: &str,
        turn: &str,
        at: i64,
        required: u64,
        provider: &str,
    ) -> Result<()> {
        let global = self.quota(at)?;
        let local = self.provider_quota(provider, at)?;
        let reset = [&global, &local]
            .iter()
            .filter(|q| q["active"] == true && q["remaining"].as_u64().unwrap_or(0) < required)
            .filter_map(|q| q["reset_at"].as_i64())
            .max()
            .unwrap_or(at);
        self.0.execute_batch("SAVEPOINT routed_wait")?;
        let result = (|| -> Result<()> {
            self.wait(scope, turn, at, required)?;
            self.0.execute(
                "UPDATE waits SET reset_at=? WHERE scope=?",
                params![reset, scope],
            )?;
            self.0.execute("INSERT INTO wait_routes(scope,provider) VALUES (?,?) ON CONFLICT(scope) DO UPDATE SET provider=excluded.provider",params![scope,provider])?;
            Ok(())
        })();
        match result {
            Ok(()) => {
                self.0.execute_batch("RELEASE routed_wait")?;
                Ok(())
            }
            Err(e) => {
                let _ = self
                    .0
                    .execute_batch("ROLLBACK TO routed_wait; RELEASE routed_wait");
                Err(e)
            }
        }
    }
    pub fn provider_wait_ready(
        &self,
        scope: &str,
        at: i64,
        required: u64,
        released: bool,
    ) -> Result<bool> {
        let provider: Option<String> = self
            .0
            .query_row(
                "SELECT provider FROM wait_routes WHERE scope=?",
                [scope],
                |r| r.get(0),
            )
            .optional()?;
        let Some(provider) = provider else {
            return Ok(true);
        };
        let (_, p) = self.provider_policy(&provider)?;
        let q = self.provider_quota(&provider, at)?;
        Ok(q["active"] != true
            || ((p.auto_resume || released) && q["remaining"].as_u64().unwrap_or(0) >= required))
    }
}
#[derive(Deserialize)]
pub struct ProviderPolicyRequest {
    provider: String,
    revision: u64,
    policy: Policy,
}
pub async fn inspect(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    agent::initialize(&s).map_err(internal)?;
    let profiles = agent::profiles(&s.inner.lock().unwrap().store).map_err(internal)?;
    let ids: std::collections::BTreeSet<_> = profiles.iter().map(|p| p.provider.clone()).collect();
    let ledger = s.usage.lock().unwrap();
    let mut entries = vec![];
    for id in ids {
        let mut quota = ledger.provider_quota(&id, now()).map_err(internal)?;
        quota["enforcement"] = json!(if profiles
            .iter()
            .filter(|p| p.provider == id)
            .all(|p| p.engine == agent::Engine::Pi)
        {
            "pre_request"
        } else {
            "reported_only"
        });
        entries.push(quota);
    }
    Ok(Json(json!({"providers":entries})))
}
pub async fn configure(
    State(s): State<Service>,
    h: HeaderMap,
    Json(r): Json<ProviderPolicyRequest>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    let profiles = agent::profiles(&s.inner.lock().unwrap().store).map_err(internal)?;
    if !profiles
        .iter()
        .any(|p| p.provider == r.provider && p.engine == agent::Engine::Pi)
    {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "provider_allowance",
            "Only configured gateway providers support a hard allowance",
        ));
    }
    let ledger = s.usage.lock().unwrap();
    if ledger.provider_policy(&r.provider).map_err(internal)?.0 != r.revision {
        return Err(error(
            StatusCode::CONFLICT,
            "provider_revision",
            "Provider allowance changed; refresh before saving",
        ));
    }
    ledger
        .set_provider_policy(&r.provider, r.revision, &r.policy)
        .map_err(|e| {
            error(
                StatusCode::BAD_REQUEST,
                "provider_allowance",
                &e.to_string(),
            )
        })?;
    Ok(Json(
        ledger
            .provider_quota(&r.provider, now())
            .map_err(internal)?,
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn spec(provider: &str) -> agent::ResolvedSpec {
        let mut profile = agent::Profile::legacy(&crate::pi::Config::default());
        profile.model = "fixture".into();
        let mut spec = profile.resolve().unwrap();
        spec.provider = provider.into();
        spec
    }
    #[test]
    fn provider_reservations_are_shared_isolated_durable_and_wait_for_both_policies() {
        let path = std::env::temp_dir().join(format!("pot-provider-{}.sqlite", id()));
        let at = 1_800_000_000_000;
        let a = spec("a");
        let b = spec("b");
        {
            let ledger = Ledger::open(&path).unwrap();
            let policy = Policy {
                period: "day".into(),
                limit: 100,
                timezone: "UTC".into(),
                auto_resume: false,
            };
            ledger.set_provider_policy("a", 0, &policy).unwrap();
            assert!(ledger.set_provider_policy("a", 0, &policy).is_err());
            assert_eq!(
                ledger
                    .reserve_routed("one", "main", "t", at, &a, 1, 30, 60)
                    .unwrap(),
                Some(60)
            );
            assert_eq!(
                ledger
                    .reserve_routed("two", "worker", "t", at, &a, 1, 10, 10)
                    .unwrap(),
                None
            );
            assert_eq!(
                ledger
                    .reserve_routed("other", "other", "t", at, &b, 1, 30, 60)
                    .unwrap(),
                Some(60)
            );
            assert_eq!(ledger.provider_quota("a", at).unwrap()["reserved"], 90);
            ledger.wait_routed("worker", "t", at, 11, "a").unwrap();
            assert!(!ledger.ready("worker", at).unwrap());
            ledger
                .settle("one", &json!({"prompt_tokens":20,"completion_tokens":10}))
                .unwrap();
            // Manual policy still requires release even after available capacity increases.
            assert!(!ledger.ready("worker", at).unwrap());
            ledger
                .0
                .execute("UPDATE waits SET released=1 WHERE scope='worker'", [])
                .unwrap();
            assert!(ledger.ready("worker", at).unwrap());
            ledger
                .set_policy(&Policy {
                    period: "day".into(),
                    limit: 100,
                    timezone: "UTC".into(),
                    auto_resume: true,
                })
                .unwrap();
            assert!(!ledger.ready("worker", at).unwrap());
            ledger.rejected("other").unwrap();
            assert!(ledger.ready("worker", at).unwrap());
            // Global allowance caps output even when this provider still has room.
            assert_eq!(
                ledger
                    .reserve_routed("three", "worker", "t", at, &a, 1, 20, 100)
                    .unwrap(),
                Some(50)
            );
            assert_eq!(ledger.provider_quota("a", at).unwrap()["remaining"], 0);
            ledger.wait_routed("paused", "t", at, 11, "a").unwrap();
        }
        let ledger = Ledger::open(&path).unwrap();
        assert_eq!(ledger.provider_quota("a", at).unwrap()["reserved"], 70);
        assert!(!ledger.ready("paused", at).unwrap());
        let reset = ledger.provider_quota("a", at).unwrap()["reset_at"]
            .as_i64()
            .unwrap();
        assert!(!ledger.ready("paused", reset).unwrap());
        ledger
            .0
            .execute("UPDATE waits SET released=1 WHERE scope='paused'", [])
            .unwrap();
        assert!(ledger.ready("paused", reset).unwrap());
        ledger.clear_wait("paused").unwrap();
        let count: i64 = ledger
            .0
            .query_row(
                "SELECT COUNT(*) FROM wait_routes WHERE scope='paused'",
                [],
                |r| r.get(0),
            )
            .unwrap();
        assert_eq!(count, 0);
        drop(ledger);
        std::fs::remove_file(path).unwrap();
    }
    #[test]
    fn failed_route_rolls_back_request_and_preserves_prior_wait() {
        let ledger = Ledger::open(std::path::Path::new(":memory:")).unwrap();
        ledger.wait_routed("main", "t", 0, 1, "a").unwrap();
        ledger.route("duplicate", "a", 1, "hash").unwrap();
        assert!(
            ledger
                .reserve_routed("duplicate", "main", "t", 0, &spec("a"), 1, 1, 1)
                .is_err()
        );
        let requests: i64 = ledger
            .0
            .query_row("SELECT COUNT(*) FROM requests", [], |r| r.get(0))
            .unwrap();
        assert_eq!(requests, 0);
        assert!(ledger.waiting("main").unwrap().is_some());
        assert_eq!(
            ledger
                .0
                .query_row(
                    "SELECT provider FROM wait_routes WHERE scope='main'",
                    [],
                    |r| r.get::<_, String>(0)
                )
                .unwrap(),
            "a"
        );
    }
    #[tokio::test]
    async fn provider_settings_require_auth_known_gateway_and_current_revision() {
        let s = crate::tests::service();
        let request = || ProviderPolicyRequest {
            provider: "deepseek".into(),
            revision: 0,
            policy: Policy::default(),
        };
        assert!(
            configure(State(s.clone()), HeaderMap::new(), Json(request()))
                .await
                .is_err()
        );
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        // No configured provider in this fixture.
        assert!(
            configure(State(s.clone()), h.clone(), Json(request()))
                .await
                .is_err()
        );
        assert!(inspect(State(s), h).await.is_ok());
    }
}
