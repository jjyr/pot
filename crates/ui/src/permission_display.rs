//! Capability approval copy shared by native and Web clients.
use crate::localization::Catalog;
use serde_json::Value;
pub fn description(record: &Value, catalog: Catalog) -> String {
    let scope = &record["scope"];
    if scope.is_null() {
        return format!(
            "{}\n\n{}",
            catalog.text("permissions.instructions"),
            record["request"]["args"]["text"].as_str().unwrap_or("")
        );
    }
    let mut description = format!(
        "{} · {}\n{}\n\n{}",
        record["assistant_id"].as_str().unwrap_or(""),
        record["machine_name"]
            .as_str()
            .or_else(|| scope["machine_id"].as_str())
            .unwrap_or(""),
        catalog.text(if scope["capability"] == "host" {
            "permissions.host-request"
        } else {
            "permissions.sandbox-request"
        }),
        catalog.text(if scope["capability"] == "host" {
            "permissions.host-detail"
        } else {
            "permissions.sandbox-detail"
        })
    );
    if scope["capability"] == "host" {
        description.push_str(&format!(
            "\n\n{}\n{}",
            record["request"]["tool"].as_str().unwrap_or(""),
            serde_json::to_string_pretty(&record["request"]["args"]).unwrap_or_default()
        ));
    }
    description
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn machine_cards_never_display_command_approvals() {
        let r = serde_json::json!({"assistant_id":"ABC","scope":{"machine_id":"Mac-Dev","capability":"sandbox"},"request":{"args":{"cmd":"secret command"}}});
        let text = description(&r, Catalog::new("en"));
        assert!(
            text.contains("ABC") && text.contains("Mac-Dev") && !text.contains("secret command")
        );
    }
}
