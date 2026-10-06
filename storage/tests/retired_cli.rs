use pot_core::*;
use pot_storage::{Store, bindings::*};
use serde_json::{Value, json};

struct LegacyConfig;
impl LegacyProfileConfig for LegacyConfig {
    fn model(&self) -> &str {
        "deepseek-v4-flash"
    }
    fn max_tokens(&self) -> u64 {
        256
    }
}

fn profile() -> Profile {
    serde_json::from_value(json!({"id":"codex-default","revision":1,"name":"Codex",
        "engine":"codex","provider":"openai","model":"fixture","auth_ref":"cli:codex",
        "params":{"max_output_tokens":256,"reasoning_effort":null},
        "tool_policy":{"worker_orchestration":false,"summary_read_only":true,"unattended":"guest_vm"}})).unwrap()
}

#[test]
fn retired_active_binding_is_archived_without_rebinding_or_losing_supported_profiles() {
    let path = std::env::temp_dir().join(format!("pot-retire-{}.sqlite", pot_storage::id()));
    let mut store = Store::open(&path).unwrap();
    let p = profile();
    select_initial(
        &mut store,
        "main",
        path.parent().unwrap(),
        p.resolve().unwrap(),
    )
    .unwrap();
    let mut old_profile = serde_json::to_value(&p).unwrap();
    old_profile["engine"] = json!("claude");
    old_profile["id"] = json!("old-cli");
    let mut api = Profile::legacy(&LegacyConfig);
    api.id = "claude-api".into();
    api.provider = "anthropic".into();
    api.auth_ref = "providers.json:anthropic".into();
    store
        .set(PROFILES, &json!([p, api, old_profile.clone()]).to_string())
        .unwrap();
    let mut old: Value = serde_json::from_str(&store.get(BINDINGS).unwrap().unwrap()).unwrap();
    old[0]["spec"]["engine"] = json!("agy");
    old[0]["native_ref"] = json!({"engine":"agy","conversation_id":"preserve-identity"});
    let original = old.to_string();
    store.set(BINDINGS, &original).unwrap();
    store.set("pi.paused", "preserve-checkpoint").unwrap();
    store.set("agent.default.worker", "old-cli").unwrap();
    store
        .set(
            "agent.migration.pending",
            &json!({"target":old[0]}).to_string(),
        )
        .unwrap();
    store.set("tasks.v2", "preserve-tasks").unwrap();
    drop(store);
    for _ in 0..2 {
        let mut store = Store::open(&path).unwrap();
        initialize_store(&mut store, "main", path.parent().unwrap(), &LegacyConfig).unwrap();
        assert_eq!(profiles(&store).unwrap(), vec![profile(), api.clone()]);
        assert!(current(&store).unwrap().is_none());
        assert_eq!(
            store.get("retired-cli.v1/agent.bindings.v1").unwrap(),
            Some(original.clone())
        );
        assert_eq!(
            store.get("retired-cli.v1/pi.paused").unwrap().as_deref(),
            Some("preserve-checkpoint")
        );
        assert!(store.get("agent.migration.pending").unwrap().is_none());
        assert!(
            store
                .get("retired-cli.v1/agent.migration.pending")
                .unwrap()
                .is_some()
        );
        assert_eq!(
            store.get("agent.retired.active").unwrap().as_deref(),
            Some("true")
        );
        // A stale worker default stays explicit: it must not fall back to another Agent.
        assert_eq!(
            store.get("agent.default.worker").unwrap().as_deref(),
            Some("old-cli")
        );
        assert_eq!(
            store.get("tasks.v2").unwrap().as_deref(),
            Some("preserve-tasks")
        );
    }
    for engine in ["claude", "agy"] {
        old_profile["engine"] = json!(engine);
        assert!(serde_json::from_value::<Profile>(old_profile.clone()).is_err());
    }
    let mut store = Store::open(&path).unwrap();
    select_initial(
        &mut store,
        "main",
        path.parent().unwrap(),
        profile().resolve().unwrap(),
    )
    .unwrap();
    drop(store);
    let store = Store::open(&path).unwrap();
    assert_eq!(current(&store).unwrap().unwrap().spec.engine, Engine::Codex);
    assert_eq!(
        store.get("agent.retired.active").unwrap().as_deref(),
        Some("false")
    );
    drop(store);
    std::fs::remove_file(path).unwrap();
}

#[test]
fn retired_history_does_not_change_the_current_supported_binding() {
    let path =
        std::env::temp_dir().join(format!("pot-retire-history-{}.sqlite", pot_storage::id()));
    let mut store = Store::open(&path).unwrap();
    select_initial(
        &mut store,
        "main",
        path.parent().unwrap(),
        profile().resolve().unwrap(),
    )
    .unwrap();
    let original = current(&store).unwrap().unwrap();
    let mut old = serde_json::to_value(&original).unwrap();
    old["spec"]["engine"] = json!("claude");
    old["state"] = json!("archived");
    old["epoch"] = json!(2);
    store
        .set(BINDINGS, &json!([original, old]).to_string())
        .unwrap();
    drop(store);
    let store = Store::open(&path).unwrap();
    assert_eq!(current(&store).unwrap(), Some(original));
    assert_ne!(
        store.get("agent.retired.active").unwrap().as_deref(),
        Some("true")
    );
    drop(store);
    std::fs::remove_file(path).unwrap();
}
