//! Native aggregation. Every row keeps its original Control and assistant identity.
use crate::backend::Requests;
use pot_protocol::{SchedulePage, TaskPage};
use serde_json::{Value, json};
use std::{
    collections::{BTreeMap, BTreeSet},
    sync::{Arc, Mutex, OnceLock},
    time::Duration,
};

use pot_ui::application::spaces::{Directory, Space};
static DIRECTORY: OnceLock<Arc<Mutex<Directory>>> = OnceLock::new();
static REFRESH: OnceLock<Vec<crate::client::Refresh>> = OnceLock::new();
static NATIVE: OnceLock<Mutex<BTreeMap<String, BTreeSet<String>>>> = OnceLock::new();
pub fn take_native_changes() -> BTreeMap<String, BTreeSet<String>> {
    std::mem::take(&mut *NATIVE.get_or_init(Default::default).lock().unwrap())
}
pub fn refresh_directory() {
    if let Some(senders) = REFRESH.get() {
        for sender in senders {
            let _ = sender.try_send(());
        }
    }
}
pub fn directory() -> Arc<Mutex<Directory>> {
    DIRECTORY
        .get_or_init(|| {
            let shared = Arc::new(Mutex::new(Directory::default()));
            let mut senders = vec![];
            for mode in ["local", "remote"] {
                let (sender, rx) = std::sync::mpsc::sync_channel(1);
                let trigger = crate::client::Refresh {
                    sender,
                    dirty: Arc::new(Mutex::new(None)),
                };
                senders.push(trigger.clone());
                let _ = trigger.try_send(());
                let state = shared.clone();
                std::thread::spawn(move || {
                    let mut subscription = None;
                    while rx.recv().is_ok() {
                        if mode == "remote"
                            && crate::backend::control_projection()["configured"] != true
                        {
                            continue;
                        }
                        if subscription.is_none() {
                            let notifications = trigger.clone();
                            let status = state.clone();
                            let mut versions = Value::Null;
                            subscription = Some(crate::backend::subscribe(
                                mode.into(),
                                "/api/v1/changes".into(),
                                move |frame| {
                                    match frame {
                                        Ok(frame) => {
                                            let next: Value = serde_json::from_str(&frame.data)
                                                .map_err(|e| e.to_string())?;
                                            let all = versions["boot_id"] != next["boot_id"];
                                            let topics: BTreeSet<String> = if all {
                                                ["machines", "settings", "resources"]
                                                    .into_iter()
                                                    .map(str::to_owned)
                                                    .collect()
                                            } else {
                                                next["versions"]
                                                    .as_object()
                                                    .into_iter()
                                                    .flatten()
                                                    .filter(|(k, v)| {
                                                        versions["versions"][*k] != **v
                                                    })
                                                    .map(|(k, _)| k.clone())
                                                    .collect()
                                            };
                                            NATIVE
                                                .get_or_init(Default::default)
                                                .lock()
                                                .unwrap()
                                                .entry(mode.into())
                                                .or_default()
                                                .extend(topics.clone());
                                            if all {
                                                let _ = notifications.try_send(());
                                            } else {
                                                notifications.changed(topics);
                                            }
                                            versions = next;
                                            crate::wake_ui();
                                        }
                                        Err(error) => {
                                            versions = Value::Null;
                                            if let Some(space) =
                                                status.lock().unwrap().spaces.get_mut(mode)
                                            {
                                                space.connected = false;
                                                space.error = error;
                                            }
                                            crate::wake_ui();
                                        }
                                    }
                                    Ok(())
                                },
                            ));
                        }
                        let dirty = trigger.dirty.lock().unwrap().replace(BTreeSet::new());
                        let (mut next, query) = {
                            let state = state.lock().unwrap();
                            (
                                state.spaces.get(mode).cloned().unwrap_or_else(|| Space {
                                    mode: mode.into(),
                                    ..Default::default()
                                }),
                                state.usage_query.clone(),
                            )
                        };
                        let generation = crate::backend::configuration_generation();
                        let result = refresh(&mut next, query, dirty.as_ref());
                        if generation != crate::backend::configuration_generation() {
                            let _ = trigger.try_send(());
                            continue;
                        }
                        match result {
                            Ok(()) => next.error.clear(),
                            Err(error) => {
                                next.error = error;
                                next.connected = false;
                            }
                        }
                        let retry = !next.connected;
                        state.lock().unwrap().spaces.insert(mode.into(), next);
                        crate::wake_ui();
                        if retry {
                            std::thread::sleep(Duration::from_secs(1));
                            let _ = trigger.try_send(());
                        }
                    }
                });
            }
            let _ = REFRESH.set(senders);
            shared
        })
        .clone()
}
fn read<T: serde::de::DeserializeOwned>(agent: &Requests, path: &str) -> Result<T, String> {
    agent
        .get(path)
        .call()?
        .body_mut()
        .read_json()
        .map_err(|e| e.to_string())
}
fn refresh(
    space: &mut Space,
    usage_query: Option<(u32, String)>,
    dirty: Option<&BTreeSet<String>>,
) -> Result<(), String> {
    let changed = |domain: &str| {
        dirty.is_none_or(|topics| {
            topics
                .iter()
                .any(|t| t == domain || t.starts_with(&format!("{domain}/")))
        })
    };
    let agent = Requests::for_mode(&space.mode)
        .scope("main")
        .timeout_global(Some(Duration::from_secs(3)));
    space.connected = false;
    let runtime: Value = read(&agent, "/api/v1/runtime")?;
    let id = runtime["runtime_id"]
        .as_str()
        .ok_or("Space identity missing")?
        .to_owned();
    if space.id != id {
        *space = Space {
            mode: space.mode.clone(),
            id: id.clone(),
            ..Default::default()
        };
    }
    space.connected = true;
    space.name = if space.mode == "local" {
        crate::i18n::text("spaces.this-mac").into()
    } else {
        crate::backend::control_projection()["url"]
            .as_str()
            .and_then(|url| url::Url::parse(url).ok())
            .and_then(|url| url.host_str().map(str::to_owned))
            .unwrap_or_else(|| id.clone())
    };
    if changed("assistants") {
        space.assistants = read(&agent, "/api/v1/assistants")?;
    }
    if changed("machines") {
        space.machines = read(&agent, "/api/v1/machines")?;
    }
    if changed("tasks") {
        let mut tasks = vec![];
        for scope in ["active", "attention", "archived"] {
            let mut offset = 0;
            loop {
                let page: TaskPage = agent
                    .get("/api/v1/assistant-tasks")
                    .query("scope", scope)
                    .query("offset", offset.to_string())
                    .query("limit", "50")
                    .call()?
                    .body_mut()
                    .read_json()
                    .map_err(|e| e.to_string())?;
                tasks.extend(page.items);
                match page.next_offset {
                    Some(next) if next > offset => offset = next,
                    _ => break,
                }
            }
        }
        // A task can move between scopes while the pages are fetched.
        tasks.sort_by_key(|a| std::cmp::Reverse(a.updated_at_ms));
        let mut seen = std::collections::BTreeSet::new();
        tasks.retain(|t| seen.insert((t.assistant_id.clone(), t.id.clone())));
        space.tasks = tasks;
    }
    let mut schedules = vec![];
    for assistant in space.assistants["assistants"]
        .as_array()
        .into_iter()
        .flatten()
    {
        let Some(owner) = assistant["id"].as_str() else {
            continue;
        };
        let scoped = Requests::for_mode(&space.mode)
            .scope(owner)
            .timeout_global(Some(Duration::from_secs(3)));
        if changed("permissions") || changed("assistants") {
            space
                .permissions
                .insert(owner.into(), read(&scoped, "/api/v1/permissions")?);
        }
        if changed("mail") || changed("assistants") {
            let mut records = Vec::new();
            let mut after = 0;
            loop {
                let page: Vec<pot_protocol::message::Record> = scoped
                    .post(pot_protocol::message::API)
                    .send_json(json!({"operation":"history","assistant_id":owner,"after":after}))?
                    .body_mut()
                    .read_json()
                    .map_err(|e| e.to_string())?;
                let next = page.last().map(|r| r.sequence);
                records.extend(page);
                match next {
                    Some(next) if next > after => after = next,
                    _ => break,
                }
            }
            space.mail.insert(owner.into(), records);
        }
        if changed("schedules") || changed("assistants") {
            let mut offset = 0;
            loop {
                let page: SchedulePage = scoped
                    .get("/api/v1/schedules")
                    .query("offset", offset.to_string())
                    .call()?
                    .body_mut()
                    .read_json()
                    .map_err(|e| e.to_string())?;
                schedules.extend(page.items.into_iter().map(|p| (owner.to_owned(), p)));
                match page.next_offset {
                    Some(next) if next > offset => offset = next,
                    _ => break,
                }
            }
        }
    }
    if changed("schedules") || changed("assistants") {
        space.schedules = schedules;
    }
    if let Some((days, timezone)) = &usage_query
        && (changed("usage") || space.usage_query != usage_query)
    {
        let mut offset = 0;
        let mut models = vec![];
        let mut summary = Value::Null;
        loop {
            let page: Value = agent
                .get("/api/v1/usage/summary")
                .query("scope", "")
                .query("days", days.to_string())
                .query("timezone", timezone)
                .query("offset", offset.to_string())
                .call()?
                .body_mut()
                .read_json()
                .map_err(|e| e.to_string())?;
            if offset == 0 {
                summary = page.clone();
            }
            models.extend(
                page["agent_models"]
                    .as_array()
                    .into_iter()
                    .flatten()
                    .cloned(),
            );
            match page["agent_model_next_offset"].as_u64() {
                Some(next) if next > offset => offset = next,
                _ => break,
            }
        }
        summary["agent_models"] = json!(models);
        space.usage = summary;
        space.usage_query = usage_query;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn embedded_and_remote_space_catalogue_contract() {
        if std::env::var_os("POT_SPACE_CATALOGUE_CHILD").is_some() {
            assert!(pot_ui::application::platform::Platform::aggregates_spaces(
                &crate::platform::Native::new()
            ));
            let mut local = Space {
                mode: "local".into(),
                ..Default::default()
            };
            refresh(&mut local, Some((7, "UTC".into())), None).unwrap();
            assert!(!local.id.is_empty());
            assert_eq!(local.assistants["assistants"].as_array().unwrap().len(), 1);
            assert!(local.error.is_empty());
            let mut remote = Space {
                mode: "remote".into(),
                ..Default::default()
            };
            refresh(&mut remote, Some((7, "UTC".into())), None).unwrap();
            assert_ne!(local.id, remote.id);
            assert_eq!(remote.assistants["assistants"].as_array().unwrap().len(), 2);
            assert_eq!(remote.tasks.len(), 1);
            assert_eq!(remote.schedules[0].0, "research");
            assert!(remote.permissions.contains_key("research"));
            assert_eq!(remote.usage["total"]["input"], 1200);
            let root = crate::client::root();
            let error = crate::backend::configuration_request(
                "remote", "research", &local.id, "/api/v1/providers",
                Some(json!({"request_id":"configuration-context-fence","name":"wrong-control-draft"})),
            ).err().expect("configuration must stay bound to its source Control");
            assert!(error.contains("identity changed"));
            assert!(
                !std::fs::read_to_string(root.join("fixture-writes.jsonl"))
                    .unwrap_or_default()
                    .contains("wrong-control-draft")
            );
            let mut reply = crate::backend::configuration_request(
                "remote",
                "research",
                &remote.id,
                "/api/v1/providers",
                None,
            )
            .unwrap();
            assert!(
                !reply.body_mut().read_json::<Value>().unwrap()["connections"]
                    .as_array()
                    .unwrap()
                    .is_empty()
            );
            std::fs::write(root.join("replacement-id"), "replacement-control").unwrap();
            let error = Requests::for_mode("remote")
                .scope("main")
                .post("/api/v1/assistants")
                .send_json(json!({"request_id":"identity-fence","name":"must-not-be-created"}))
                .err()
                .expect("changed identity must reject mutation");
            assert!(error.contains("identity changed"));
            assert!(
                !std::fs::read_to_string(root.join("fixture-writes.jsonl"))
                    .unwrap()
                    .contains("must-not-be-created")
            );
            return;
        }
        use std::{
            io::{BufRead, BufReader},
            process::{Command, Stdio},
        };
        let root =
            std::env::temp_dir().join(format!("pot-space-catalogue-{}", uuid::Uuid::new_v4()));
        let script = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../scripts/tests/multi-space-fixture.py");
        let mut fixture = Command::new("python3")
            .arg(script)
            .args(["--port", "0", "--root"])
            .arg(&root)
            .stdout(Stdio::piped())
            .spawn()
            .unwrap();
        let mut ready = String::new();
        BufReader::new(fixture.stdout.take().unwrap())
            .read_line(&mut ready)
            .unwrap();
        assert!(ready.contains("Remote fixture"));
        let mut child = Command::new(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "spaces::tests::embedded_and_remote_space_catalogue_contract",
                "--nocapture",
            ])
            .env("POT_SPACE_CATALOGUE_CHILD", "1")
            .env("POT_LOCAL_ROOT", &root)
            .env("POT_SECRET_KEY_FILE", root.join("test-key"))
            .spawn()
            .unwrap();
        let deadline = std::time::Instant::now() + Duration::from_secs(45);
        let status = loop {
            if let Some(status) = child.try_wait().unwrap() {
                break Some(status);
            }
            if std::time::Instant::now() > deadline {
                let _ = child.kill();
                let _ = child.wait();
                break None;
            }
            std::thread::sleep(Duration::from_millis(50));
        };
        let _ = fixture.kill();
        let _ = fixture.wait();
        let _ = std::fs::remove_dir_all(root);
        assert!(
            status.is_some_and(|s| s.success()),
            "isolated catalogue contract failed"
        );
    }
}
