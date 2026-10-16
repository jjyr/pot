use super::*;
#[derive(Deserialize)]
pub struct ProviderPolicyRequest {
    provider: String,
    revision: u64,
    policy: Policy,
}
pub async fn inspect(State(s): State<Service>, h: HeaderMap) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    agent::initialize(&s).map_err(internal)?;
    let profiles = agent::configured_profiles(&s).map_err(internal)?;
    let ids: std::collections::BTreeSet<_> = profiles.iter().map(|p| p.provider.clone()).collect();
    let ledger = s.usage.lock().unwrap();
    let mut entries = vec![];
    for id in ids {
        let mut quota = ledger.provider_quota(&id, now()).map_err(internal)?;
        quota["enforcement"] = json!("pre_request");
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
    let profiles = agent::configured_profiles(&s).map_err(internal)?;
    if !profiles.iter().any(|p| p.provider == r.provider) {
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
        let mut profile = agent::Profile::api(&crate::execution::Config::default());
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
        ledger.route("duplicate", &spec("a"), 1).unwrap();
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
