use super::*;
fn scope(capability: Capability) -> Scope {
    Scope {
        machine_id: "mac-dev".into(),
        capability,
    }
}
fn operation(owner: &str, call: &str) -> Request {
    Request {
        owner: owner.into(),
        environment: "host".into(),
        generation: 1,
        turn: "turn".into(),
        call_id: call.into(),
        tool: "exec_command".into(),
        args: json!({"cmd":"true"}),
    }
}
fn key(assistant: &str, work: &str, capability: Capability) -> WorkKey {
    WorkKey {
        assistant: assistant.into(),
        work: work.into(),
        scope: scope(capability),
    }
}
#[test]
fn sandbox_permission_deduplicates_tasks_and_covers_future_sandboxes_only_in_this_work() {
    let hub = Hub::memory();
    let one = hub
        .join(
            operation("main", "1"),
            "abc",
            "work",
            Some(scope(Capability::Sandbox)),
        )
        .unwrap();
    let two = hub
        .join(
            operation("task-a", "2"),
            "abc",
            "work",
            Some(scope(Capability::Sandbox)),
        )
        .unwrap();
    assert_eq!(one, two);
    hub.decide("abc", &one, "allow_once").unwrap();
    hub.leave(&one);
    assert_eq!(hub.decision(&two), Some(true));
    hub.leave(&two);
    let book = hub.book.lock().unwrap();
    assert!(book.requests.is_empty());
    assert_eq!(
        Hub::allowed(&book, &key("abc", "work", Capability::Sandbox)),
        Some(true)
    );
    assert_eq!(
        Hub::allowed(&book, &key("abc", "next", Capability::Sandbox)),
        None
    );
    assert_eq!(
        Hub::allowed(&book, &key("abc", "work", Capability::Host)),
        None
    );
    assert_eq!(
        Hub::allowed(&book, &key("other", "work", Capability::Sandbox)),
        None
    );
}
#[test]
fn denial_is_shared_by_work_and_does_not_block_future_work() {
    let hub = Hub::memory();
    let pending = hub
        .join(
            operation("main", "1"),
            "abc",
            "work",
            Some(scope(Capability::Host)),
        )
        .unwrap();
    assert!(hub.decide("other", &pending, "allow_persistent").is_err());
    hub.decide("abc", &pending, "deny").unwrap();
    hub.leave(&pending);
    let book = hub.book.lock().unwrap();
    assert_eq!(
        Hub::allowed(&book, &key("abc", "work", Capability::Host)),
        Some(false)
    );
    assert_eq!(
        Hub::allowed(&book, &key("abc", "next", Capability::Host)),
        None
    );
}
#[test]
fn persistent_access_survives_restart_and_revocation_applies_to_session_checks() {
    let dir = std::env::temp_dir().join(format!("machine-permissions-{}", id()));
    std::fs::create_dir(&dir).unwrap();
    let path = dir.join("permissions.json");
    let hub = Hub::open(&path).unwrap();
    let pending = hub
        .join(
            operation("task", "1"),
            "abc",
            "work",
            Some(scope(Capability::Host)),
        )
        .unwrap();
    hub.decide("abc", &pending, "allow_persistent").unwrap();
    let restored = Hub::open(&path).unwrap();
    assert_eq!(
        Hub::allowed(
            &restored.book.lock().unwrap(),
            &key("abc", "new-work", Capability::Host)
        ),
        Some(true)
    );
    restored.decide("abc", &pending, "revoke").unwrap();
    assert_eq!(
        Hub::allowed(
            &restored.book.lock().unwrap(),
            &key("abc", "new-work", Capability::Host)
        ),
        None
    );
    assert!(
        Hub::open(&path)
            .unwrap()
            .book
            .lock()
            .unwrap()
            .grants
            .is_empty()
    );
    let config: Value = serde_json::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    assert_eq!(config["version"], 2);
    assert!(config.get("temporary").is_none());
    std::fs::remove_dir_all(dir).unwrap();
}
#[test]
fn configured_deny_overrides_saved_and_temporary_access() {
    let hub = Hub::memory();
    let pending = hub
        .join(
            operation("main", "1"),
            "abc",
            "work",
            Some(scope(Capability::Host)),
        )
        .unwrap();
    hub.decide("abc", &pending, "allow_once").unwrap();
    let mut book = hub.book.lock().unwrap();
    book.policy.rules.push(pot_agent::permissions::Rule {
        assistant_id: "abc".into(),
        scope: scope(Capability::Host),
        decision: DefaultDecision::Deny,
    });
    assert_eq!(
        Hub::allowed(&book, &key("abc", "work", Capability::Host)),
        Some(false)
    );
}
#[test]
fn sandbox_ownership_and_limits_are_enforced_without_operation_approvals() {
    let dir = std::env::temp_dir().join(format!("sandbox-permissions-{}", id()));
    std::fs::create_dir(&dir).unwrap();
    let hub = Hub::open(&dir.join("permissions.json")).unwrap();
    hub.record_sandbox("abc", "mac-dev", "dev").unwrap();
    assert!(hub.sandbox_access("abc", "mac-dev", "dev", true).is_ok());
    assert!(
        hub.sandbox_access("other", "mac-dev", "dev", false)
            .is_err()
    );
    assert!(hub.record_sandbox("other", "mac-dev", "dev").is_err());
    let config = json!({"action":"update","config":{"cpus":4,"memory_gib":8,"disk_gib":32}});
    hub.validate_sandbox_change("mac-dev", &config).unwrap();
    hub.validate_sandbox_creation("mac-dev", config["config"].clone(), 15)
        .unwrap();
    assert!(
        hub.validate_sandbox_creation("mac-dev", config["config"].clone(), 16)
            .is_err()
    );
    assert!(
        hub.validate_sandbox_creation("mac-dev", json!({"cpus":4,"memory_gib":8,"disk_gib":64}), 0)
            .is_err()
    );
    hub.validate_sandbox_creation("mac-dev", json!({"cpus":4,"memory_gib":8,"disk_gib":0}), 0)
        .unwrap();
    let mut escape = config.clone();
    escape["config"]["privileged"] = json!(true);
    assert!(hub.validate_sandbox_change("mac-dev", &escape).is_err());
    let mut large = config;
    large["config"]["memory_gib"] = json!(1000);
    assert!(hub.validate_sandbox_change("mac-dev", &large).is_err());
    assert!(
        hub.validate_sandbox_change("mac-dev", &json!({"action":"set_mounts","mappings":[]}))
            .is_err()
    );
    std::fs::remove_dir_all(dir).unwrap();
}
#[test]
fn cancelled_waiter_cannot_cancel_another_tasks_shared_request() {
    let hub = Hub::memory();
    let a = hub
        .join(
            operation("main", "1"),
            "abc",
            "work",
            Some(scope(Capability::Sandbox)),
        )
        .unwrap();
    let b = hub
        .join(
            operation("task", "2"),
            "abc",
            "work",
            Some(scope(Capability::Sandbox)),
        )
        .unwrap();
    hub.leave(&a);
    assert_eq!(hub.decision(&b), None);
    hub.decide("abc", &b, "allow_once").unwrap();
    assert_eq!(hub.decision(&b), Some(true));
    hub.leave(&b);
    assert!(hub.book.lock().unwrap().requests.is_empty());
}
#[test]
fn instruction_review_never_creates_machine_grants() {
    let hub = Hub::memory();
    let mut request = operation("main", "1");
    request.tool = "update_agent_instructions".into();
    let p = hub.join(request, "abc", "work", None).unwrap();
    assert!(hub.decide("abc", &p, "allow_persistent").is_err());
    hub.decide("abc", &p, "allow_once").unwrap();
    assert_eq!(hub.decision(&p), Some(true));
    hub.leave(&p);
    assert!(hub.book.lock().unwrap().grants.is_empty());
}

#[test]
fn re_enrollment_invalidates_grants_and_preallow_policy() {
    let hub = Hub::memory();
    hub.sync_machine_epoch("mac-dev", "first").unwrap();
    let p = hub
        .join(
            operation("main", "1"),
            "abc",
            "work",
            Some(scope(Capability::Host)),
        )
        .unwrap();
    hub.decide("abc", &p, "allow_persistent").unwrap();
    hub.book
        .lock()
        .unwrap()
        .policy
        .rules
        .push(pot_agent::permissions::Rule {
            assistant_id: "abc".into(),
            scope: scope(Capability::Sandbox),
            decision: DefaultDecision::Allow,
        });
    hub.sync_machine_epoch("mac-dev", "second").unwrap();
    let book = hub.book.lock().unwrap();
    assert!(book.grants.is_empty() && book.policy.rules.is_empty());
    assert_eq!(
        Hub::allowed(&book, &key("abc", "work", Capability::Host)),
        None
    );
}
