use super::*;
fn scope() -> Scope {
    Scope {
        machine_id: "devpc".into(),
        capability: Capability::Host,
    }
}
fn key(assistant: &str, work: &str) -> WorkKey {
    WorkKey {
        assistant: assistant.into(),
        work: work.into(),
        scope: scope(),
    }
}
fn operation(call: &str) -> Request {
    Request {
        owner: "task".into(),
        execution_target: "host".into(),
        generation: 1,
        turn: "turn".into(),
        call_id: call.into(),
        tool: "exec_command".into(),
        args: json!({"cmd":"true"}),
    }
}
#[test]
fn machine_policy_applies_to_every_assistant_and_deny_overrides_prior_approval() {
    let hub = Hub::memory();
    let mut book = hub.book.lock().unwrap();
    assert_eq!(Hub::allowed(&book, &key("a", "work")), None);
    book.policy.machines.insert(
        "devpc".into(),
        pot_agent::permissions::MachinePolicy {
            execution_mode: pot_agent::permissions::ExecutionMode::Host,
            host_access: DefaultDecision::Allow,
        },
    );
    for owner in ["a", "b"] {
        assert_eq!(Hub::allowed(&book, &key(owner, "work")), Some(true));
    }
    book.temporary.insert(key("a", "work"), true);
    book.policy.machines.get_mut("devpc").unwrap().host_access = DefaultDecision::Deny;
    assert_eq!(Hub::allowed(&book, &key("a", "work")), Some(false));
}
#[test]
fn host_requests_do_not_merge_different_commands_or_call_ids() {
    let hub = Hub::memory();
    let first = operation("one");
    let a = hub
        .join(first.clone(), "a", "work:one", Some(scope()))
        .unwrap();
    let mut changed = first.clone();
    changed.args["cmd"] = json!("different");
    let b = hub
        .join(changed, "a", "work:changed", Some(scope()))
        .unwrap();
    let c = hub
        .join(operation("two"), "a", "work:two", Some(scope()))
        .unwrap();
    assert_ne!(a, b);
    assert_ne!(a, c);
    assert!(hub.decide("b", &a, "allow_once").is_err());
    assert!(hub.decide("a", &a, "allow_persistent").is_err());
    hub.decide("a", &a, "allow_once").unwrap();
    assert_eq!(hub.decision(&a), Some(true));
    assert_eq!(hub.decision(&b), None);
    assert_eq!(hub.decision(&c), None);
}
#[test]
fn machine_configuration_persists_but_operation_decisions_do_not() {
    let dir = std::env::temp_dir().join(format!("pot-machine-policy-{}", id()));
    let path = dir.join("permissions.json");
    let hub = Hub::open(&path).unwrap();
    let mut book = hub.book.lock().unwrap();
    let mut policy = book.policy.clone();
    policy.machines.insert(
        "devpc".into(),
        pot_agent::permissions::MachinePolicy {
            execution_mode: pot_agent::permissions::ExecutionMode::Host,
            host_access: DefaultDecision::Allow,
        },
    );
    hub.save_policy(&mut book, policy).unwrap();
    drop(book);
    let restored = Hub::open(&path).unwrap();
    assert!(restored.continuous_host("any-assistant", "devpc"));
    let value: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    assert_eq!(value["version"], 4);
    assert!(value.get("grants").is_none());
    assert!(value["policy"].get("rules").is_none());
    assert!(value["policy"].get("sandbox_limits").is_none());
    std::fs::remove_dir_all(dir).unwrap();
}
#[test]
fn reenrollment_resets_machine_trust() {
    let hub = Hub::memory();
    hub.sync_machine_epoch("devpc", "first").unwrap();
    hub.book.lock().unwrap().policy.machines.insert(
        "devpc".into(),
        pot_agent::permissions::MachinePolicy {
            execution_mode: pot_agent::permissions::ExecutionMode::Host,
            host_access: DefaultDecision::Allow,
        },
    );
    hub.sync_machine_epoch("devpc", "first").unwrap();
    assert!(hub.continuous_host("a", "devpc"));
    hub.sync_machine_epoch("devpc", "replacement").unwrap();
    assert!(!hub.continuous_host("a", "devpc"));
}
#[test]
fn instruction_edits_always_require_their_own_decision() {
    let hub = Hub::memory();
    let a = hub.join(operation("a"), "owner", "work", None).unwrap();
    let b = hub.join(operation("b"), "owner", "work", None).unwrap();
    assert_ne!(a, b);
    hub.decide("owner", &a, "allow_once").unwrap();
    assert_eq!(hub.decision(&a), Some(true));
    assert_eq!(hub.decision(&b), None);
}
