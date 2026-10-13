//! Assistant-owned instructions. Execution execution targets never own this record.
use crate::*;
use pot_assistant::instructions::load;

fn ensure_assistant(s: &Service) -> Result<()> {
    anyhow::ensure!(
        !s.is_worker && s.usage_scope == s.assistant_id,
        "Only the owning assistant can access assistant instructions"
    );
    Ok(())
}
pub(crate) fn read(s: &Service) -> Result<Value> {
    ensure_assistant(s)?;
    let inner = s.conversation.inner.lock().unwrap();
    let rules = load(&inner.store)?;
    Ok(json!({"text":rules.text,"revision":rules.revision}))
}
pub(crate) fn save(s: &Service, text: &str, expected_revision: &str) -> Result<Value> {
    ensure_assistant(s)?;
    let inner = s.conversation.inner.lock().unwrap();
    let rules = pot_assistant::instructions::save(&inner.store, text, expected_revision)?;
    Ok(json!({"text":rules.text,"revision":rules.revision}))
}

pub(crate) fn validate_tool(s: &Service, name: &str, args: &Value) -> Result<()> {
    ensure_assistant(s)?;
    let fields: &[&str] = match name {
        "read_agent_instructions" => &[],
        "update_agent_instructions" => &["text"],
        _ => bail!("Unknown assistant instructions tool"),
    };
    anyhow::ensure!(
        args.as_object()
            .is_some_and(|o| o.keys().all(|k| fields.contains(&k.as_str()))),
        "Invalid assistant instructions arguments"
    );
    if name == "update_agent_instructions" {
        anyhow::ensure!(
            args["text"]
                .as_str()
                .is_some_and(|text| text.len() <= 262144),
            "text is required and must fit within 256 KiB"
        );
    }
    Ok(())
}
pub(crate) fn execute(
    s: &Service,
    name: &str,
    args: &Value,
    approval_revision: Option<&str>,
) -> Result<Value> {
    validate_tool(s, name, args)?;
    match name {
        "read_agent_instructions" => read(s),
        "update_agent_instructions" => save(
            s,
            args["text"].as_str().unwrap(),
            approval_revision
                .ok_or_else(|| anyhow!("Instructions update was not prepared for review"))?,
        ),
        _ => unreachable!(),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn db_survives_reopen_and_revisions_prevent_aba() {
        let s = tasks::tests::fixture();
        let original = read(&s).unwrap();
        assert_eq!(original["text"], "");
        let changed = save(&s, "Changed rules", original["revision"].as_str().unwrap()).unwrap();
        let reverted = save(&s, "", changed["revision"].as_str().unwrap()).unwrap();
        assert_ne!(reverted["revision"], original["revision"]);
        assert!(save(&s, "Stale write", original["revision"].as_str().unwrap()).is_err());
        s.conversation.inner.lock().unwrap().store =
            Store::open(&s.data.join("pot-v2.sqlite")).unwrap();
        assert_eq!(read(&s).unwrap(), reverted);
    }
    #[test]
    fn caller_cannot_supply_another_owner_and_workers_have_no_record() {
        let s = tasks::tests::fixture();
        let original = read(&s).unwrap();
        let args = json!({"text":"replacement","assistant_id":"other"});
        assert!(execute(&s, "update_agent_instructions", &args, None).is_err());
        assert_eq!(read(&s).unwrap(), original);
        let mut worker = s.clone();
        worker.is_worker = true;
        assert!(read(&worker).is_err());
        assert!(save(&worker, "rules", original["revision"].as_str().unwrap()).is_err());
    }
    #[test]
    fn assistant_rules_do_not_import_old_files_or_preferences() {
        let s = tasks::tests::fixture();
        let old = s
            .agent_service
            .resources
            .home
            .join("assistants")
            .join(&s.runtime_id)
            .join(&s.assistant_id)
            .join("AGENTS.md");
        std::fs::create_dir_all(old.parent().unwrap()).unwrap();
        std::fs::write(&old, "OLD_FILE_RULE").unwrap();
        s.conversation
            .inner
            .lock()
            .unwrap()
            .store
            .set(
                "agent.execution_preferences.v1",
                &json!({"text":"OLD_PREFERENCE_RULE","revision":1}).to_string(),
            )
            .unwrap();
        let hub = crate::assistants::Hub::open(s.clone()).unwrap();
        assert_eq!(hub.rules(&s.assistant_id).unwrap()["text"], "");
        assert_eq!(std::fs::read_to_string(old).unwrap(), "OLD_FILE_RULE");
    }

    #[tokio::test]
    async fn assistant_db_rules_and_project_files_refresh_independently() {
        let root = tasks::tests::fixture();
        let initial = read(&root).unwrap();
        let saved = save(
            &root,
            "ASSISTANT_DB_ONLY",
            initial["revision"].as_str().unwrap(),
        )
        .unwrap();
        let project = root.execution_service.sandboxes.workspace("default");
        let host_project = root.data.join("agent-workspace");
        std::fs::create_dir_all(&host_project).unwrap();
        std::fs::write(host_project.join("AGENTS.md"), "HOST_FIRST").unwrap();
        std::fs::create_dir_all(&project).unwrap();
        std::fs::write(project.join(".git"), "gitdir").unwrap();
        std::fs::write(project.join("AGENTS.md"), "PROJECT_FIRST").unwrap();
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: id(),
                text: "Explicit task brief".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let worker = tasks::owner(&root, &task.id).unwrap();
        let main = crate::instructions::load(&root).unwrap().text;
        let child = crate::instructions::load(&worker).unwrap().text;
        assert!(main.contains("ASSISTANT_DB_ONLY") && main.contains("HOST_FIRST"));
        assert!(child.contains("PROJECT_FIRST") && !child.contains("ASSISTANT_DB_ONLY"));
        std::fs::write(project.join("AGENTS.md"), "PROJECT_CHANGED").unwrap();
        std::fs::write(host_project.join("AGENTS.md"), "HOST_CHANGED").unwrap();
        for (service, expected) in [(&root, "HOST_CHANGED"), (&worker, "PROJECT_CHANGED")] {
            let text = crate::instructions::load(service).unwrap().text;
            assert!(text.contains(expected) && !text.contains("PROJECT_FIRST"));
        }
        assert_eq!(read(&root).unwrap(), saved);
        save(
            &root,
            "ASSISTANT_DB_CHANGED",
            saved["revision"].as_str().unwrap(),
        )
        .unwrap();
        assert_eq!(
            std::fs::read_to_string(project.join("AGENTS.md")).unwrap(),
            "PROJECT_CHANGED"
        );
        std::fs::remove_file(project.join("AGENTS.md")).unwrap();
        std::fs::remove_file(host_project.join("AGENTS.md")).unwrap();
        let main = crate::instructions::load(&root).unwrap().text;
        assert!(main.contains("ASSISTANT_DB_CHANGED") && !main.contains("PROJECT_CHANGED"));
        assert!(
            !crate::instructions::load(&worker)
                .unwrap()
                .text
                .contains("ASSISTANT_DB_CHANGED")
        );
    }

    #[tokio::test]
    async fn host_project_instructions_remain_filesystem_based_without_sandboxes() {
        let mut root = tasks::tests::fixture();
        let empty = root.data.join("empty-inventory");
        root.execution_service.sandboxes =
            Arc::new(crate::sandbox::Manager::empty(&empty, vec![]).unwrap());
        let rules = read(&root).unwrap();
        save(
            &root,
            "HOST_ASSISTANT_DB",
            rules["revision"].as_str().unwrap(),
        )
        .unwrap();
        std::fs::write(root.workspace.join(".git"), "gitdir").unwrap();
        std::fs::write(root.workspace.join("AGENTS.md"), "HOST_PROJECT_FILE").unwrap();
        let sub = root.workspace.join("sub");
        std::fs::create_dir_all(&sub).unwrap();
        std::fs::write(sub.join("AGENTS.md"), "HOST_DEEPER_PROJECT_FILE").unwrap();
        root.workspace = sub;
        let text = crate::instructions::load(&root).unwrap().text;
        assert!(text.contains("HOST_DEEPER_PROJECT_FILE"));
        assert!(
            text.find("HOST_PROJECT_FILE").unwrap()
                < text.find("HOST_DEEPER_PROJECT_FILE").unwrap()
        );
        assert!(text.contains("HOST_ASSISTANT_DB") && text.contains("HOST_PROJECT_FILE"));
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: id(),
                text: "Host task".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        assert!(matches!(
            task.execution_target.unwrap().target,
            pot_core::ExecutionTargetKind::Host {}
        ));
        let worker = tasks::owner(&root, &task.id).unwrap();
        std::fs::write(worker.workspace.join("AGENTS.md"), "HOST_TASK_PROJECT_FILE").unwrap();
        let child = crate::instructions::load(&worker).unwrap().text;
        assert!(child.contains("HOST_TASK_PROJECT_FILE"));
        assert!(!child.contains("HOST_ASSISTANT_DB"));
    }

    #[tokio::test]
    async fn worker_does_not_inherit_assistant_or_global_instructions() {
        let root = tasks::tests::fixture();
        let rules = read(&root).unwrap();
        save(
            &root,
            "ASSISTANT_PRIVATE_RULE",
            rules["revision"].as_str().unwrap(),
        )
        .unwrap();
        std::fs::create_dir_all(&root.agent_service.resources.home).unwrap();
        std::fs::write(
            root.agent_service.resources.home.join("AGENTS.md"),
            "GLOBAL_PRIVATE_RULE",
        )
        .unwrap();
        let task = tasks::create(
            State(root.clone()),
            tasks::tests::headers(),
            Json(CreateTask {
                request_id: id(),
                text: "Explicit task brief".into(),
                ..Default::default()
            }),
        )
        .await
        .unwrap()
        .0;
        let worker = tasks::owner(&root, &task.id).unwrap();
        std::fs::write(worker.workspace.join("AGENTS.md"), "PROJECT_DIRECTORY_RULE").unwrap();
        let main = crate::instructions::load(&root).unwrap().text;
        assert!(main.contains("ASSISTANT_PRIVATE_RULE"));
        let injected = crate::instructions::load(&worker).unwrap().text;
        assert!(!injected.contains("ASSISTANT_PRIVATE_RULE"));
        assert!(!injected.contains("GLOBAL_PRIVATE_RULE"));
        assert!(injected.contains("PROJECT_DIRECTORY_RULE"));
        assert!(
            worker
                .conversation
                .inner
                .lock()
                .unwrap()
                .store
                .get("assistant.instructions.v1")
                .unwrap()
                .is_none()
        );
    }
}
