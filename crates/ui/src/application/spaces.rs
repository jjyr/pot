use pot_protocol::{ScheduleInfo, TaskInfo, TaskPage, TaskQuery};
use serde_json::{Value, json};
use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
};
#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Owner {
    pub control: String,
    pub mode: String,
    pub assistant: String,
}
impl Owner {
    pub fn key(&self) -> String {
        format!("{}/{}", self.control, self.assistant)
    }
    pub fn object(&self, id: &str) -> String {
        format!("{}/{id}", self.key())
    }
}
#[derive(Clone, Default, PartialEq)]
pub struct Space {
    pub mode: String,
    pub id: String,
    pub name: String,
    pub assistants: Value,
    pub machines: Value,
    pub permissions: BTreeMap<String, Value>,
    pub tasks: Vec<TaskInfo>,
    pub schedules: Vec<(String, ScheduleInfo)>,
    pub usage: Value,
    pub mail: BTreeMap<String, Vec<pot_protocol::message::Record>>,
    pub usage_query: Option<(u32, String)>,
    pub error: String,
    pub connected: bool,
}
impl Space {
    pub fn owner(&self, assistant: &str) -> Owner {
        Owner {
            control: self.id.clone(),
            mode: self.mode.clone(),
            assistant: assistant.into(),
        }
    }
    pub fn assistant_name(&self, id: &str) -> &str {
        self.assistants["assistants"]
            .as_array()
            .into_iter()
            .flatten()
            .find(|a| a["id"] == id)
            .and_then(|a| a["name"].as_str())
            .unwrap_or("Pot")
    }
    pub fn label(&self) -> String {
        format!(
            "{} · {}",
            super::i18n::text(if self.mode == "local" {
                "spaces.local"
            } else {
                "spaces.cloud"
            }),
            self.name
        )
    }
}
#[derive(Clone, Default, PartialEq)]
pub struct Directory {
    pub spaces: BTreeMap<String, Space>,
    pub usage_query: Option<(u32, String)>,
}
impl Directory {
    pub fn assistants(&self) -> Value {
        let mut items = vec![];
        for s in self.spaces.values() {
            for assistant in s.assistants["assistants"].as_array().into_iter().flatten() {
                let mut a = assistant.clone();
                a["id"] = json!(s.owner(a["id"].as_str().unwrap_or("main")).key());
                a["space_label"] = json!(s.label());
                a["space_mode"] = json!(s.mode);
                items.push(a);
            }
        }
        json!({"assistants":items})
    }
    pub fn task_locations(&self) -> Value {
        let mut labels = serde_json::Map::new();
        for s in self.spaces.values() {
            for task in &s.tasks {
                if let Some(execution_target) = task
                    .execution_target
                    .as_ref()
                    .and_then(|e| serde_json::to_value(e).ok())
                {
                    let machine = s.machines["machines"]
                        .as_array()
                        .into_iter()
                        .flatten()
                        .find(|m| m["runtime_id"] == execution_target["runtime_id"])
                        .and_then(|m| m["name"].as_str())
                        .unwrap_or_else(|| execution_target["runtime_id"].as_str().unwrap_or(""));
                    let target = if execution_target["target"]["kind"] == "host" {
                        "Host"
                    } else {
                        execution_target["target"]["sandbox_id"]
                            .as_str()
                            .unwrap_or("Sandbox")
                    };
                    labels.insert(
                        s.owner(&task.assistant_id).object(&task.id),
                        json!(format!("{machine} / {target}")),
                    );
                }
            }
        }
        Value::Object(labels)
    }
    pub fn task_owner(&self, key: &str) -> Option<(Owner, String)> {
        self.spaces.values().find_map(|s| {
            s.tasks.iter().find_map(|t| {
                let owner = s.owner(&t.assistant_id);
                (owner.object(&t.id) == key).then(|| (owner, t.id.clone()))
            })
        })
    }
    pub fn task_page(&self, query: &TaskQuery) -> TaskPage {
        let mut page = TaskPage::default();
        let search = query.q.to_lowercase();
        for s in self.spaces.values() {
            for t in &s.tasks {
                match t.phase.scope() {
                    "active" => page.counts.active += 1,
                    "attention" => page.counts.attention += 1,
                    _ => page.counts.archived += 1,
                }
                if t.phase.scope() != query.scope
                    || (!search.is_empty()
                        && !t.title.to_lowercase().contains(&search)
                        && !t.id.to_lowercase().contains(&search))
                {
                    continue;
                }
                if !query.execution_target.is_empty() {
                    let filter: Value =
                        serde_json::from_str(&query.execution_target).unwrap_or(Value::Null);
                    let execution_target = if filter.get("space_id").is_some() {
                        if filter["space_id"] != s.id {
                            continue;
                        }
                        &filter["execution_target"]
                    } else {
                        &filter
                    };
                    if t.execution_target
                        .as_ref()
                        .and_then(|e| serde_json::to_value(e).ok())
                        .as_ref()
                        != Some(execution_target)
                    {
                        continue;
                    }
                }
                let mut task = t.clone();
                let owner = s.owner(&task.assistant_id);
                task.id = owner.object(&task.id);
                task.assistant_id = owner.key();
                page.items.push(task);
            }
        }
        page.items.sort_by(|a, b| {
            b.updated_at_ms
                .cmp(&a.updated_at_ms)
                .then_with(|| a.id.cmp(&b.id))
        });
        page.total = page.items.len();
        let limit = query.limit.max(1);
        page.offset = query
            .offset
            .min(page.total.saturating_sub(1) / limit * limit);
        page.next_offset = (page.offset + limit < page.total).then_some(page.offset + limit);
        page.items = page
            .items
            .into_iter()
            .skip(page.offset)
            .take(limit)
            .collect();
        page
    }
    pub fn error(&self) -> String {
        self.spaces
            .values()
            .filter(|s| !s.error.is_empty())
            .map(|s| format!("{}: {}", s.label(), s.error))
            .collect::<Vec<_>>()
            .join("\n")
    }
    pub fn usage(&self, location: &str, query: &(u32, String), offset: usize) -> Value {
        let sources: Vec<_> = self
            .spaces
            .values()
            .filter(|s| location.is_empty() || s.mode == location)
            .filter(|s| s.usage_query.as_ref() == Some(query))
            .map(|s| s.usage.clone())
            .collect();
        merge_usage(&sources, offset)
    }
}
pub fn merge_usage(sources: &[Value], offset: usize) -> Value {
    let Some(first) = sources.first() else {
        return Value::Null;
    };
    let mut result = first.clone();
    for key in [
        "total",
        "lifetime",
        "historical",
        "routing_total",
        "compaction_total",
    ] {
        let mut total = json!({});
        for field in ["requests", "input", "output", "cache", "unknown"] {
            total[field] = json!(
                sources
                    .iter()
                    .map(|v| v[key][field].as_u64().unwrap_or(0))
                    .sum::<u64>()
            );
        }
        result[key] = total;
    }
    let mut models: BTreeMap<(String, String), Value> = BTreeMap::new();
    let mut buckets: BTreeMap<String, Value> = BTreeMap::new();
    for source in sources {
        for row in source["agent_models"].as_array().into_iter().flatten() {
            let key = (
                row["agent"].as_str().unwrap_or("").into(),
                row["model"].as_str().unwrap_or("").into(),
            );
            add_row(
                models
                    .entry(key)
                    .or_insert_with(|| json!({"agent":row["agent"],"model":row["model"]})),
                row,
            );
        }
        for row in source["buckets"].as_array().into_iter().flatten() {
            add_row(
                buckets
                    .entry(row["range"].as_str().unwrap_or("").into())
                    .or_insert_with(|| json!({"label":row["label"],"range":row["range"]})),
                row,
            );
        }
    }
    let mut rows: Vec<_> = models.into_values().collect();
    rows.sort_by(|a, b| {
        tokens(b)
            .cmp(&tokens(a))
            .then_with(|| a.to_string().cmp(&b.to_string()))
    });
    result["agent_model_next_offset"] = json!((offset + 20 < rows.len()).then_some(offset + 20));
    result["agent_models"] = json!(rows.into_iter().skip(offset).take(20).collect::<Vec<_>>());
    result["buckets"] = json!(buckets.into_values().collect::<Vec<_>>());
    result["quota"] = Value::Null;
    result["waiting"] = json!([]);
    result["offset"] = json!(offset);
    result
}
fn tokens(v: &Value) -> u64 {
    v["input"].as_u64().unwrap_or(0) + v["output"].as_u64().unwrap_or(0)
}
fn add_row(total: &mut Value, row: &Value) {
    for field in ["requests", "input", "output", "cache", "unknown"] {
        total[field] = json!(total[field].as_u64().unwrap_or(0) + row[field].as_u64().unwrap_or(0));
    }
}

/// Only replies in conversations initiated from this assistant's user chat are
/// surfaced. Unrelated background exchanges stay in the mailbox.
pub fn chat_updates(
    records: &[pot_protocol::message::Record],
    owner: &Owner,
) -> Vec<pot_protocol::message::Record> {
    let initiated: std::collections::BTreeSet<_> = records
        .iter()
        .filter(|r| {
            r.message.hops == 0
                && r.message.from.control_id == owner.control
                && r.message.from.assistant_id == owner.assistant
        })
        .map(|r| r.message.conversation_id.as_str())
        .collect();
    let mut latest = BTreeMap::new();
    for record in records {
        let message = &record.message;
        if initiated.contains(message.conversation_id.as_str())
            && message.to.control_id == owner.control
            && message.to.assistant_id == owner.assistant
        {
            latest.insert(message.conversation_id.clone(), record.clone());
        }
    }
    let mut items: Vec<_> = latest.into_values().collect();
    items.sort_by_key(|r| r.sequence);
    items
}

pub fn directory() -> Arc<Mutex<Directory>> {
    super::platform::with(|p| p.directory())
}
pub fn refresh_directory() {
    super::platform::with(|p| p.refresh_directory())
}
pub fn take_native_changes()
-> std::collections::BTreeMap<String, std::collections::BTreeSet<String>> {
    super::platform::with(|p| p.changes())
}

#[cfg(test)]
mod tests {
    use super::*;
    fn task(id: &str, assistant: &str, updated: i64) -> TaskInfo {
        serde_json::from_value(json!({"id":id,"assistant_id":assistant,"title":format!("Work {id}"),"phase":"running","pending":0,"created_at_ms":0,"updated_at_ms":updated,"resource":"","sandbox_id":""})).unwrap()
    }
    fn directory_fixture() -> Directory {
        Directory {
            spaces: [
                (
                    "local".into(),
                    Space {
                        mode: "local".into(),
                        id: "local-id".into(),
                        tasks: vec![task("same", "same", 30), task("older", "same", 10)],
                        ..Default::default()
                    },
                ),
                (
                    "remote".into(),
                    Space {
                        mode: "remote".into(),
                        id: "remote-id".into(),
                        tasks: vec![task("same", "same", 20)],
                        error: "offline".into(),
                        ..Default::default()
                    },
                ),
            ]
            .into(),
            ..Default::default()
        }
    }
    #[test]
    fn merged_pages_are_global_and_duplicate_ids_route_to_their_original_control() {
        let d = directory_fixture();
        let mut q = TaskQuery {
            limit: 2,
            ..Default::default()
        };
        let first = d.task_page(&q);
        assert_eq!(first.total, 3);
        assert_eq!(first.counts.active, 3);
        assert_eq!(first.next_offset, Some(2));
        assert_eq!(first.items[0].id, "local-id/same/same");
        assert_eq!(first.items[1].id, "remote-id/same/same");
        let (owner, id) = d.task_owner(&first.items[1].id).unwrap();
        assert_eq!(owner.mode, "remote");
        assert_eq!(id, "same");
        assert!(d.task_owner("same").is_none());
        q.offset = 2;
        assert_eq!(d.task_page(&q).items[0].id, "local-id/same/older");
        q.q = "same".into();
        q.offset = 100;
        let page = d.task_page(&q);
        assert_eq!(page.total, 2);
        assert_eq!(page.offset, 0);
        // Offline rows remain inspectable, never remapped to the online Space.
        assert!(d.error().contains("offline"));
    }
    #[test]
    fn machine_filter_keeps_space_identity_when_execution_ids_match() {
        let mut d = directory_fixture();
        let execution_target = json!({"runtime_id":"same-machine", "target":{"kind":"host"}});
        for space in d.spaces.values_mut() {
            for task in &mut space.tasks {
                task.execution_target =
                    Some(serde_json::from_value(execution_target.clone()).unwrap());
            }
        }
        let query = TaskQuery {
            execution_target: json!({"space_id":"remote-id", "execution_target":execution_target})
                .to_string(),
            ..Default::default()
        };
        let page = d.task_page(&query);
        assert_eq!(page.total, 1);
        assert_eq!(page.items[0].id, "remote-id/same/same");
    }
    #[test]
    fn changed_control_identity_cannot_resolve_a_cached_object() {
        let mut d = directory_fixture();
        d.spaces.get_mut("remote").unwrap().id = "replacement".into();
        assert!(d.task_owner("remote-id/same/same").is_none());
        assert!(d.task_owner("local-id/same/same").is_some());
    }
    #[test]
    fn usage_merges_raw_buckets_and_models_before_pagination() {
        let a = json!({"total":{"input":10,"output":2,"unknown":1},"agent_models":[{"agent":"a","model":"m","input":10,"output":2}],"buckets":[{"range":"2026-01-01","label":"01-01","input":10}]});
        let b = json!({"total":{"input":20,"output":3,"unknown":2},"agent_models":[{"agent":"a","model":"m","input":20,"output":3}],"buckets":[{"range":"2026-01-01","label":"01-01","input":20}]});
        let merged = merge_usage(&[a, b], 0);
        assert_eq!(merged["total"]["input"], 30);
        assert_eq!(merged["total"]["unknown"], 3);
        assert_eq!(merged["agent_models"].as_array().unwrap().len(), 1);
        assert_eq!(merged["agent_models"][0]["output"], 5);
        assert_eq!(merged["buckets"][0]["input"], 30);
        assert!(merged["quota"].is_null());
        assert!(merge_usage(&[], 0).is_null());
    }
    #[test]
    fn different_usage_range_is_unknown_instead_of_reusing_cached_totals() {
        let mut d = directory_fixture();
        let s = d.spaces.get_mut("local").unwrap();
        s.usage = json!({"total":{"input":123}});
        s.usage_query = Some((7, "UTC".into()));
        assert!(d.usage("local", &(30, "UTC".into()), 0).is_null());
        assert_eq!(
            d.usage("local", &(7, "UTC".into()), 0)["total"]["input"],
            123
        );
    }
    #[test]
    fn only_latest_reply_to_user_initiated_collaboration_is_shown_in_chat() {
        use pot_protocol::message::{AssistantRef, Envelope, Record};
        let owner = Owner {
            control: "local".into(),
            mode: "local".into(),
            assistant: "a".into(),
        };
        let from = AssistantRef {
            control_id: "local".into(),
            assistant_id: "a".into(),
        };
        let to = AssistantRef {
            control_id: "remote".into(),
            assistant_id: "b".into(),
        };
        let root = Record {
            sequence: 1,
            message: Envelope {
                id: "root".into(),
                from: from.clone(),
                to: to.clone(),
                conversation_id: "c".into(),
                reply_to: None,
                text: "work".into(),
                hops: 0,
            },
            state: "delivered".into(),
            detail: None,
        };
        let mut reply = root.clone();
        reply.sequence = 2;
        reply.message.from = to;
        reply.message.to = from;
        reply.message.hops = 1;
        reply.message.id = "reply".into();
        let mut unrelated = reply.clone();
        unrelated.sequence = 3;
        unrelated.message.conversation_id = "other".into();
        let mut latest = reply.clone();
        latest.sequence = 4;
        latest.message.id = "latest".into();
        let items = chat_updates(&[root, reply, unrelated, latest], &owner);
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].message.id, "latest");
    }
}
