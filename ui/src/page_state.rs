//! Shared page transitions independent of navigation, transport and GPUI.
use pot_protocol::{TaskPage, TaskQuery};
use serde_json::{Value, json};
pub fn task_query(
    kind: &str,
    value: &Value,
    query: &TaskQuery,
    page: &TaskPage,
) -> Option<TaskQuery> {
    let mut query = query.clone();
    match kind {
        "task-filter" => {
            let scope = value.as_str()?;
            if !["active", "attention", "archived"].contains(&scope) {
                return None;
            }
            query.scope = scope.into();
            query.offset = 0;
        }
        "task-clear-search" => {
            query.q.clear();
            query.offset = 0;
        }
        "task-previous" => query.offset = query.offset.saturating_sub(query.limit),
        "task-next" => query.offset = page.next_offset?,
        _ => return None,
    }
    Some(query)
}
pub fn sandbox_selection(inventory: &Value, preferred: &str) -> String {
    let items = inventory["items"].as_array();
    let fallback = inventory["selected"].as_str().unwrap_or("default");
    items
        .and_then(|items| {
            items
                .iter()
                .find(|i| i["id"] == preferred)
                .or_else(|| items.iter().find(|i| i["id"] == fallback))
                .or_else(|| items.first())
        })
        .and_then(|item| item["id"].as_str())
        .unwrap_or("")
        .to_owned()
}
pub fn sandbox_view(inventory: &Value, selected: &str) -> Value {
    let phase = inventory["items"]
        .as_array()
        .and_then(|items| items.iter().find(|i| i["id"] == selected))
        .and_then(|i| i["state"]["phase"].as_str())
        .unwrap_or("unknown");
    json!({"sandboxes":inventory["items"],"sandbox_view":selected,"sandbox_default":inventory["selected"],"phase":phase,"viewer":{"connected":false,"human":false}})
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn navigation_uses_authoritative_offsets_and_keeps_filter_and_search() {
        let query = TaskQuery {
            scope: "attention".into(),
            q: "中文".into(),
            offset: 20,
            ..Default::default()
        };
        let page = TaskPage {
            next_offset: Some(37),
            ..Default::default()
        };
        let next = task_query("task-next", &Value::Null, &query, &page).unwrap();
        assert_eq!(
            (next.offset, next.scope.as_str(), next.q.as_str()),
            (37, "attention", "中文")
        );
        assert!(task_query("task-next", &Value::Null, &query, &TaskPage::default()).is_none());
        let filtered = task_query("task-filter", &json!("archived"), &query, &page).unwrap();
        assert_eq!((filtered.offset, filtered.q.as_str()), (0, "中文"));
        assert!(task_query("task-filter", &json!("invalid"), &query, &page).is_none());
    }
    #[test]
    fn viewing_does_not_change_execution_default_and_removed_selection_falls_back() {
        let inventory = json!({"selected":"one","items":[{"id":"one","state":{"phase":"running"}},{"id":"two","state":{"phase":"stopped"}}]});
        let selected = sandbox_selection(&inventory, "two");
        let state = sandbox_view(&inventory, &selected);
        assert_eq!(state["sandbox_view"], "two");
        assert_eq!(state["sandbox_default"], "one");
        assert_eq!(state["phase"], "stopped");
        assert_eq!(sandbox_selection(&inventory, "removed"), "one");
        assert_eq!(sandbox_selection(&json!({"items":[]}), "two"), "");
    }
}
