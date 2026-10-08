//! Human-readable approval content; absent plugin metadata falls back to raw arguments.
use crate::localization::Catalog;
use serde_json::Value;

fn localized(value: &Value, catalog: Catalog) -> Option<&str> {
    value[catalog.locale()]
        .as_str()
        .or_else(|| value["en"].as_str())
}

pub fn source(request: &Value, catalog: Catalog) -> String {
    match request.get("plugin").filter(|p| p.is_object()) {
        Some(plugin) => plugin["id"].as_str().unwrap_or("").into(),
        None => catalog
            .text(
                if matches!(
                    request["tool"].as_str(),
                    Some(
                        "exec_command"
                            | "write_stdin"
                            | "apply_patch"
                            | "set_sandbox_mappings"
                            | "manage_sandbox"
                    )
                ) {
                    "permissions.builtin-tool"
                } else {
                    "permissions.unknown-source"
                },
            )
            .into(),
    }
}

pub fn heading(record: &Value, catalog: Catalog) -> String {
    let environment = record["environment_name"].as_str().unwrap_or_else(|| {
        if record["request"]["environment"] == "host" {
            "Host"
        } else {
            "Sandbox"
        }
    });
    format!(
        "{} · {}: {}",
        source(&record["request"], catalog),
        catalog.text("permissions.environment"),
        environment
    )
}

pub fn description(request: &Value, catalog: Catalog) -> Option<String> {
    if let Some(summary) = localized(&request["display"]["summary"], catalog) {
        return Some(summary.to_owned());
    }
    let tool = request["tool"].as_str()?;
    let builtin = match tool {
        "exec_command" => Some("permissions.run-command"),
        "apply_patch" => Some("permissions.edit-files"),
        "set_sandbox_mappings" => Some("permissions.map-directories"),
        "manage_sandbox" => Some(match request["args"]["action"].as_str()? {
            "create" => "permissions.create-sandbox",
            "delete" => "permissions.delete-sandbox",
            "start" => "permissions.start-sandbox",
            "stop" => "permissions.stop-sandbox",
            _ => "permissions.manage-sandbox",
        }),
        _ => None,
    };
    let summary = match builtin {
        Some(key) => catalog.text(key),
        None => localized(&request["display"]["summary"], catalog)?,
    };
    let mut text = summary.to_owned();
    for (key, value) in request["args"].as_object()? {
        let label = if builtin.is_some() {
            match key.as_str() {
                "cmd" => catalog.text("permissions.command"),
                "workdir" => catalog.text("permissions.workdir"),
                "patch" => catalog.text("permissions.patch"),
                "sandbox_id" => catalog.text("permissions.sandbox"),
                "mappings" => catalog.text("permissions.mappings"),
                "name" => catalog.text("permissions.name"),
                "action" => catalog.text("permissions.action"),
                "shell" => catalog.text("permissions.shell"),
                "login" => catalog.text("permissions.login"),
                "tty" => catalog.text("permissions.tty"),
                "timeout_ms" => catalog.text("permissions.timeout"),
                "yield_time_ms" => catalog.text("permissions.yield"),
                "max_output_chars" => catalog.text("permissions.output-limit"),
                _ => key,
            }
        } else {
            localized(&request["display"]["labels"][key], catalog).unwrap_or(key)
        };
        let mapping_text = if tool == "set_sandbox_mappings" && key == "mappings" {
            value
                .as_array()
                .and_then(|mappings| {
                    mappings
                        .iter()
                        .map(|m| {
                            if m.as_object()?.len() != 3 {
                                return None;
                            }
                            Some(format!(
                                "{} → {} ({})",
                                m["source"].as_str()?,
                                m["destination"].as_str()?,
                                catalog.text(if m["read_only"].as_bool()? {
                                    "permissions.read-only"
                                } else {
                                    "permissions.read-write"
                                })
                            ))
                        })
                        .collect::<Option<Vec<_>>>()
                })
                .map(|lines| {
                    if lines.is_empty() {
                        catalog.text("permissions.no-mappings").into()
                    } else {
                        lines.join("\n")
                    }
                })
        } else {
            None
        };
        let value = match value {
            Value::String(s) => s.clone(),
            Value::Bool(true) => catalog.text("permissions.yes").into(),
            Value::Bool(false) => catalog.text("permissions.no").into(),
            other => mapping_text
                .unwrap_or_else(|| serde_json::to_string_pretty(other).unwrap_or_default()),
        };
        text.push_str(&format!("\n\n{label}:\n{value}"));
    }
    Some(text)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn summaries_hide_plugin_arguments_and_missing_text_uses_raw_fallback() {
        let zh = Catalog::new("zh-CN");
        let shell = json!({"tool":"exec_command","args":{"cmd":"echo '$HOME'","workdir":"/tmp","tty":false}});
        let text = description(&shell, zh).unwrap();
        assert!(text.contains("是否允许运行以下命令"));
        assert!(text.contains("echo '$HOME'"));
        assert!(text.contains("工作目录:\n/tmp"));
        let mut plugin = json!({"tool":"external_tool","args":{"code":"await tab.click()"}});
        assert!(description(&plugin, zh).is_none());
        plugin["display"] = json!({"summary":{"en":"Allow browser use?","zh-CN":"是否允许使用浏览器？"},"labels":{"code":{"en":"JavaScript","zh-CN":"执行代码"}}});
        assert_eq!(description(&plugin, zh).unwrap(), "是否允许使用浏览器？");
        plugin["display"]["summary"]
            .as_object_mut()
            .unwrap()
            .remove("zh-CN");
        assert!(
            description(&plugin, zh)
                .unwrap()
                .starts_with("Allow browser use?")
        );
        let mut mapping = json!({"tool":"set_sandbox_mappings","args":{"sandbox_id":"dev","mappings":[{"source":"/work","destination":"/mnt/host/work","read_only":true}]}});
        assert!(
            description(&mapping, zh)
                .unwrap()
                .contains("/work → /mnt/host/work (只读)")
        );
        mapping["args"]["mappings"] = json!([]);
        assert!(
            description(&mapping, zh)
                .unwrap()
                .contains("移除所有目录映射")
        );
    }
    #[test]
    fn plugin_source_is_independent_of_display_text_and_fallback() {
        let zh = Catalog::new("zh-CN");
        let mut request = json!({"tool":"external_tool","plugin":{"id":"external-plugin","version":"1.0.0","digest":"hash"},"args":{},"display":{"summary":{"en":"Built-in tool","zh-CN":"内置工具"}}});
        assert_eq!(source(&request, zh), "external-plugin");
        let record = json!({"environment_name":"开发沙盒","request":request});
        assert_eq!(heading(&record, zh), "external-plugin · 环境: 开发沙盒");
        request.as_object_mut().unwrap().remove("display");
        assert_eq!(source(&request, zh), "external-plugin");
        request["plugin"]["version"] = json!("1.0\nBuilt-in tool");
        assert!(!source(&request, zh).contains('\n'));
        request.as_object_mut().unwrap().remove("plugin");
        assert_eq!(source(&request, zh), "工具来源未提供");
        request["tool"] = json!("exec_command");
        assert_eq!(source(&request, zh), "内置工具");
    }
}
