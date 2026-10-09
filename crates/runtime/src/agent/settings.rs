//! Turn-boundary configuration updates preserve the same thread context.
use super::*;
use serde::Deserialize;
const PENDING: &str = "thread.pending_config";
#[derive(Clone, serde::Serialize, Deserialize)]
pub struct PendingConfig {
    request_id: String,
    config: ResolvedSpec,
}
pub fn pending(store: &Store) -> Result<Option<PendingConfig>> {
    store
        .get(PENDING)?
        .filter(|s| !s.is_empty())
        .map(|v| Ok(serde_json::from_str(&v)?))
        .transpose()
}
fn queue(store: &mut Store, request_id: &str, config: ResolvedSpec) -> Result<()> {
    if let Some(old) = pending(store)? {
        anyhow::ensure!(
            old.request_id == request_id && old.config == config,
            "Another configuration update is pending"
        );
        return Ok(());
    }
    store.set(
        PENDING,
        &serde_json::to_string(&PendingConfig {
            request_id: request_id.into(),
            config,
        })?,
    )
}
pub fn apply_pending(s: &Service, at_turn_boundary: bool) -> Result<()> {
    let mut inner = s.inner.lock().unwrap();
    if inner.agent.phase == AgentPhase::Failed
        || (!at_turn_boundary
            && (inner.turn.is_some()
                || inner
                    .store
                    .get("execution.paused")?
                    .is_some_and(|v| !v.is_empty())))
    {
        return Ok(());
    }
    let Some(update) = pending(&inner.store)? else {
        return Ok(());
    };
    let mut thread = current(&inner.store)?.ok_or_else(|| anyhow!("Thread missing"))?;
    thread.config = update.config;
    thread.generation = thread
        .generation
        .checked_add(1)
        .ok_or_else(|| anyhow!("Execution generation overflow"))?;
    let tx = inner.store.0.transaction()?;
    storage::set(&tx, storage::THREAD, &serde_json::to_string(&thread)?)?;
    storage::set(&tx, PENDING, "")?;
    tx.commit()?;
    Ok(())
}
#[derive(Deserialize)]
pub struct Select {
    request_id: String,
    #[serde(default = "main_owner")]
    owner: String,
    profile: String,
}
fn main_owner() -> String {
    "main".into()
}
pub async fn select(
    State(root): State<Service>,
    h: HeaderMap,
    Json(request): Json<Select>,
) -> ApiResult<Json<Value>> {
    root.authorized(&h)?;
    if request.request_id.is_empty() || request.request_id.len() > 128 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_request",
            "Invalid request ID",
        ));
    }
    initialize(&root).map_err(internal)?;
    let profile = profiles(&root.inner.lock().unwrap().store)
        .map_err(internal)?
        .into_iter()
        .find(|p| p.id == request.profile)
        .ok_or_else(|| {
            error(
                StatusCode::NOT_FOUND,
                "profile_missing",
                "Profile not found",
            )
        })?;
    let config = gateway::resolve(&root, &profile).map_err(internal)?;
    gateway::validate_spec(&root, &config).map_err(internal)?;
    crate::tasks::validate_configuration(&root, &request.owner, &config)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "task_profile", &e.to_string()))?;
    let owner = crate::tasks::owner(&root, &request.owner)?;
    initialize(&owner).map_err(internal)?;
    {
        let mut inner = owner.inner.lock().unwrap();
        let key = format!("thread.config_request:{}", request.request_id);
        let fingerprint = serde_json::to_string(&config).map_err(|e| internal(e.into()))?;
        if let Some(old) = inner.store.get(&key).map_err(internal)? {
            if old != fingerprint {
                return Err(error(
                    StatusCode::CONFLICT,
                    "configuration_conflict",
                    "Request ID reused with different configuration",
                ));
            }
        } else {
            if pending(&inner.store).map_err(internal)?.is_some() {
                return Err(error(
                    StatusCode::CONFLICT,
                    "configuration_pending",
                    "Another configuration update is pending",
                ));
            }
            let tx = inner
                .store
                .0
                .transaction()
                .map_err(|e| internal(e.into()))?;
            let update = PendingConfig {
                request_id: request.request_id,
                config,
            };
            storage::set(
                &tx,
                PENDING,
                &serde_json::to_string(&update).map_err(|e| internal(e.into()))?,
            )
            .map_err(internal)?;
            storage::set(&tx, &key, &fingerprint).map_err(internal)?;
            tx.commit().map_err(|e| internal(e.into()))?;
        }
    }
    apply_pending(&owner, false).map_err(internal)?;
    Ok(Json(
        json!({"saved":true,"pending":pending(&owner.inner.lock().unwrap().store).map_err(internal)?.is_some()}),
    ))
}

#[derive(Deserialize)]
pub struct Settings {
    profile: Profile,
    #[serde(default)]
    expected_revision: u64,
    request_id: String,
    #[serde(default = "follow")]
    follow_main: bool,
    #[serde(default = "follow")]
    follow_worker: bool,
}
fn follow() -> bool {
    true
}
pub async fn settings(
    State(s): State<Service>,
    h: HeaderMap,
    Json(mut settings): Json<Settings>,
) -> ApiResult<Json<Value>> {
    s.authorized(&h)?;
    initialize(&s).map_err(internal)?;
    settings
        .profile
        .params
        .agent
        .pin_plugins()
        .map_err(|e| error(StatusCode::BAD_REQUEST, "invalid_plugin", &e.to_string()))?;
    let spec = gateway::resolve(&s, &settings.profile)
        .map_err(|e| error(StatusCode::BAD_REQUEST, "invalid_profile", &e.to_string()))?;
    gateway::validate_spec(&s, &spec).map_err(internal)?;
    let key = format!("agent.settings:{}", settings.request_id);
    let already_saved = s
        .inner
        .lock()
        .unwrap()
        .store
        .get(&key)
        .map_err(internal)?
        .is_some();
    if settings.request_id.is_empty() || settings.request_id.len() > 128 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_request",
            "Invalid request ID",
        ));
    }
    if !already_saved {
        let inner = s.inner.lock().unwrap();
        let revision = profiles(&inner.store)
            .map_err(internal)?
            .iter()
            .find(|p| p.id == settings.profile.id)
            .map(|p| p.revision)
            .unwrap_or(0);
        if revision != settings.expected_revision
            || Some(settings.profile.revision) != revision.checked_add(1)
        {
            return Err(error(
                StatusCode::CONFLICT,
                "profile_revision",
                "Profile revision changed",
            ));
        }
    }
    let mut inner = s.inner.lock().unwrap();
    let mut fingerprint = json!({"profile":settings.profile,"expected_revision":settings.expected_revision,"follow_main":settings.follow_main});
    if !settings.follow_worker {
        fingerprint["follow_worker"] = json!(false);
    }
    let fingerprint = fingerprint.to_string();
    if let Some(old) = inner.store.get(&key).map_err(internal)? {
        if old != fingerprint {
            return Err(error(
                StatusCode::CONFLICT,
                "settings_conflict",
                "Request ID reused with different settings",
            ));
        }
        return Ok(Json(json!({"saved":true,"duplicate":true})));
    }
    if settings.request_id.is_empty() || settings.request_id.len() > 128 {
        return Err(error(
            StatusCode::BAD_REQUEST,
            "invalid_request",
            "Invalid request ID",
        ));
    }
    let mut profiles = profiles(&inner.store).map_err(internal)?;
    let revision = profiles
        .iter()
        .find(|p| p.id == settings.profile.id)
        .map(|p| p.revision)
        .unwrap_or(0);
    if revision != settings.expected_revision
        || Some(settings.profile.revision) != revision.checked_add(1)
    {
        return Err(error(
            StatusCode::CONFLICT,
            "profile_revision",
            "Profile revision changed",
        ));
    }
    // Queue before saving the editable profile: a failed queue leaves settings unchanged.
    // Both changes are enclosed by one outer savepoint, including the queue's transaction.
    inner
        .store
        .0
        .execute_batch("SAVEPOINT agent_settings")
        .map_err(|e| internal(e.into()))?;
    let result = (|| -> Result<()> {
        if settings.follow_main && current(&inner.store)?.is_none() {
            select_initial(&mut inner.store, "main", spec)?;
        } else if settings.follow_main {
            queue(&mut inner.store, &settings.request_id, spec)?;
        }
        profiles.retain(|p| p.id != settings.profile.id);
        profiles.push(settings.profile.clone());
        inner
            .store
            .set(storage::PROFILES, &serde_json::to_string(&profiles)?)?;
        if settings.follow_worker {
            inner
                .store
                .set("agent.default.worker", &settings.profile.id)?;
        }
        inner.store.set(&key, &fingerprint)?;
        Ok(())
    })();
    match result {
        Ok(()) => inner.store.0.execute_batch("RELEASE agent_settings"),
        Err(e) => {
            let _ = inner
                .store
                .0
                .execute_batch("ROLLBACK TO agent_settings; RELEASE agent_settings");
            return Err(internal(e));
        }
    }
    .map_err(|e| internal(e.into()))?;
    drop(inner);
    apply_pending(&s, false).map_err(internal)?;
    if s.inner.lock().unwrap().agent.phase == AgentPhase::NeedsConfiguration && configured(&s) {
        s.phase(AgentPhase::Ready, None);
    }
    Ok(Json(json!({"saved":true,"existing_tasks":"pinned"})))
}

#[cfg(test)]
mod tests {
    use super::*;
    fn config() -> execution::Config {
        execution::Config {
            api_key: "fixture-secret".into(),
            model: "deepseek-v4-flash".into(),
            request_limit: 100,
            max_tokens: 8192,
            max_requests_per_turn: 10,
            max_parallel_tasks: 5,
        }
    }
    fn fixture() -> (PathBuf, Store, (), ResolvedSpec) {
        let dir = std::env::temp_dir().join(format!("pot-thread-{}", id()));
        std::fs::create_dir_all(&dir).unwrap();
        let mut store = Store::open(&dir.join("pot-v2.sqlite")).unwrap();
        initialize_store(&mut store, "main", &config()).unwrap();
        let mut profile = Profile::api(&config());
        profile.revision = 2;
        profile.params.max_output_tokens = 4096;
        (dir, store, (), profile.resolve().unwrap())
    }
    #[tokio::test]
    async fn following_chat_uses_active_snapshot_until_main_save_is_applied() {
        let (dir, store, _, _) = fixture();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.execution = Some(config());
        s.inner.lock().unwrap().store = store;
        let active = thread(&s).unwrap();
        let mut edited = Profile::api(&config());
        edited.revision = 2;
        edited.model = "edited-only".into();
        s.inner
            .lock()
            .unwrap()
            .store
            .set(
                storage::PROFILES,
                &serde_json::to_string(&vec![edited]).unwrap(),
            )
            .unwrap();
        s.inner
            .lock()
            .unwrap()
            .store
            .set("agent.routing.v1", &json!({"follow_chat":true}).to_string())
            .unwrap();
        let (spec, _) = routing::resolve(
            &s,
            None,
            routing::Source::User,
            &TaskRequirements::default(),
        )
        .unwrap();
        assert_eq!(spec, active.config);
        std::fs::remove_dir_all(dir).unwrap();
    }
    #[tokio::test]
    async fn profile_library_edits_preserve_defaults_and_explicit_worker_routing() {
        let (dir, store, _, _) = fixture();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.workspace = dir.clone();
        s.execution = Some(config());
        s.inner.lock().unwrap().store = store;
        let original = thread(&s).unwrap();
        let mut profile = Profile::api(&config());
        profile.id = "separate-worker".into();
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let request = json!({"profile":profile,"expected_revision":0,"request_id":"library-copy","follow_main":false,"follow_worker":false});
        for _ in 0..2 {
            let _ = settings(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(request.clone()).unwrap()),
            )
            .await
            .unwrap();
        }
        assert_eq!(thread(&s).unwrap(), original);
        assert_eq!(
            routing::resolve(
                &s,
                None,
                routing::Source::User,
                &TaskRequirements::default()
            )
            .unwrap()
            .0
            .source_profile_id,
            "pot-default"
        );
        let p = routing::Policy {
            worker_profile: Some(profile.id.clone()),
            ..Default::default()
        };
        assert!(
            routing::configure(State(s.clone()), HeaderMap::new(), Json(p.clone()))
                .await
                .is_err()
        );
        let saved = routing::configure(State(s.clone()), h.clone(), Json(p.clone()))
            .await
            .unwrap()
            .0;
        assert!(
            routing::configure(State(s.clone()), h.clone(), Json(p))
                .await
                .is_err()
        );
        let mut invalid = saved.clone();
        invalid.worker_profile = Some("missing".into());
        assert!(
            routing::configure(State(s.clone()), h.clone(), Json(invalid))
                .await
                .is_err()
        );
        let (spec, _) = routing::resolve(
            &s,
            None,
            routing::Source::User,
            &TaskRequirements::default(),
        )
        .unwrap();
        assert_eq!(spec.source_profile_id, profile.id);
        // Later connection saves may change the legacy fallback, but not an explicit role choice.
        s.inner
            .lock()
            .unwrap()
            .store
            .set("agent.default.worker", "pot-default")
            .unwrap();
        assert_eq!(
            routing::resolve(
                &s,
                None,
                routing::Source::User,
                &TaskRequirements::default()
            )
            .unwrap()
            .0,
            spec
        );
        assert_eq!(
            routing::resolve(
                &s,
                Some("pot-default"),
                routing::Source::User,
                &TaskRequirements::default()
            )
            .unwrap()
            .0
            .source_profile_id,
            "pot-default"
        );
        let restored: routing::Policy =
            serde_json::from_str(&serde_json::to_string(&routing::policy(&s).unwrap()).unwrap())
                .unwrap();
        assert_eq!(restored.worker_profile, Some(profile.id));
        let mut follow = saved;
        follow.worker_profile = None;
        let _ = routing::configure(State(s.clone()), h, Json(follow))
            .await
            .unwrap();
        assert_eq!(
            routing::resolve(
                &s,
                None,
                routing::Source::User,
                &TaskRequirements::default()
            )
            .unwrap()
            .0
            .source_profile_id,
            "pot-default"
        );
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn configuration_waits_for_turn_then_preserves_full_context_across_restart() {
        let (dir, store, _, target) = fixture();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.execution = Some(config());
        s.inner.lock().unwrap().store = store;
        let before = thread(&s).unwrap();
        let context = json!({"type":"context","version":1,"messages":[{"role":"user","text":"original goal"},{"role":"assistant","text":"working","calls":[{"id":"effect","name":"exec_command","arguments":{"cmd":"touch file"}}]},{"role":"tool","call_id":"effect","text":"done"}],"pending":[],"last_request":"first","receipts":[{"id":"effect","result":"done"}],"usage_anchor":{"tokens":123},"steering_ids":["guidance"]});
        {
            let inner = s.inner.lock().unwrap();
            save_context(&inner.store, &context).unwrap();
            inner.store.set("tasks.v2", "pending tasks").unwrap();
            inner.store.set("execution.paused", "checkpoint").unwrap();
        }
        s.turn(Some(TurnState {
            user_message_id: "running".into(),
            cancelling: false,
        }));
        queue(&mut s.inner.lock().unwrap().store, "change", target.clone()).unwrap();
        apply_pending(&s, false).unwrap();
        assert_eq!(thread(&s).unwrap(), before);
        s.turn(None);
        apply_pending(&s, false).unwrap();
        assert_eq!(thread(&s).unwrap(), before); // Paused continuation still owns its configuration.
        s.inner.lock().unwrap().store = Store::open(&dir.join("pot-v2.sqlite")).unwrap();
        apply_pending(&s, true).unwrap();
        let after = thread(&s).unwrap();
        assert_eq!(after.generation, before.generation + 1);
        assert_eq!(after.config, target);
        let inner = s.inner.lock().unwrap();
        assert_eq!(load_context(&inner.store).unwrap(), Some(context));
        assert_eq!(
            inner.store.get("tasks.v2").unwrap().as_deref(),
            Some("pending tasks")
        );
        assert_eq!(
            inner.store.get("execution.paused").unwrap().as_deref(),
            Some("checkpoint")
        );
        assert!(pending(&inner.store).unwrap().is_none());
        drop(inner);
        apply_pending(&s, true).unwrap();
        assert_eq!(thread(&s).unwrap(), after); // Already applied changes are never applied twice.
        assert!(!dir.join(".pot/handoff").exists());
        std::fs::remove_dir_all(dir).unwrap();
    }
    #[tokio::test]
    async fn profile_save_and_selection_are_idempotent_and_reject_pending_conflicts() {
        let (dir, store, _, _) = fixture();
        let mut s = crate::tests::service();
        s.data = dir.clone();
        s.execution = Some(config());
        s.inner.lock().unwrap().store = store;
        let mut h = HeaderMap::new();
        h.insert("authorization", "Bearer test-token".parse().unwrap());
        let mut p = Profile::api(&config());
        p.revision = 2;
        p.model = "next-model".into();
        let body = json!({"profile":p,"expected_revision":1,"request_id":"save","follow_main":false,"follow_worker":false});
        for _ in 0..2 {
            let _ = settings(
                State(s.clone()),
                h.clone(),
                Json(serde_json::from_value(body.clone()).unwrap()),
            )
            .await
            .unwrap();
        }
        let before = thread(&s).unwrap();
        s.turn(Some(TurnState {
            user_message_id: "running".into(),
            cancelling: false,
        }));
        let req = || Select {
            request_id: "select".into(),
            owner: "main".into(),
            profile: p.id.clone(),
        };
        assert!(
            select(State(s.clone()), HeaderMap::new(), Json(req()))
                .await
                .is_err()
        );
        for _ in 0..2 {
            assert_eq!(
                select(State(s.clone()), h.clone(), Json(req()))
                    .await
                    .unwrap()
                    .0["pending"],
                true
            );
        }
        assert_eq!(thread(&s).unwrap(), before);
        let conflict = Select {
            request_id: "conflict".into(),
            ..req()
        };
        assert!(
            select(State(s.clone()), h.clone(), Json(conflict))
                .await
                .is_err()
        );
        s.turn(None);
        apply_pending(&s, false).unwrap();
        let after = thread(&s).unwrap();
        assert_eq!(after.config.model, "next-model");
        let _ = select(State(s.clone()), h, Json(req())).await.unwrap();
        assert_eq!(thread(&s).unwrap(), after);
        std::fs::remove_dir_all(dir).unwrap();
    }
}
