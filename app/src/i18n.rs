//! Application translations. Static translations are cached once per supported locale.
use std::{collections::HashMap, sync::OnceLock};

include!(concat!(env!("OUT_DIR"), "/localized_formats.rs"));

pub fn set_locale(locale: &str) -> bool {
    let locale = if locale == "zh-CN" { "zh-CN" } else { "en" };
    if &*rust_i18n::locale() == locale {
        return false;
    }
    gpui_kit::component::set_locale(locale);
    true
}

pub fn is_chinese() -> bool {
    &*rust_i18n::locale() == "zh-CN"
}

fn translations() -> &'static HashMap<String, [String; 2]> {
    static TRANSLATIONS: OnceLock<HashMap<String, [String; 2]>> = OnceLock::new();
    TRANSLATIONS.get_or_init(|| {
        let keys: HashMap<String, String> =
            serde_json::from_str(include_str!("../locales/en.json")).expect("valid translations");
        keys.into_keys()
            .map(|key| {
                let en = rust_i18n::t!(&key, locale = "en").into_owned();
                let zh = rust_i18n::t!(&key, locale = "zh-CN").into_owned();
                (key, [en, zh])
            })
            .collect()
    })
}

pub fn text(key: &str) -> &'static str {
    translations()
        .get(key)
        .map(|v| v[usize::from(is_chinese())].as_str())
        .unwrap_or("Missing translation")
}

pub fn matches_message(key: &str, value: &str) -> bool {
    translations()
        .get(key)
        .is_some_and(|values| values.iter().any(|translation| translation == value))
}

/// Translate recognized application notices only; user/model content never calls this.
pub fn message(value: &str) -> String {
    if value.is_empty() {
        return String::new();
    }
    static SOURCES: OnceLock<HashMap<String, String>> = OnceLock::new();
    let sources = SOURCES.get_or_init(|| {
        let backend: Vec<String> =
            serde_json::from_str(include_str!("../localization-message-keys.json")).unwrap();
        let mut result = HashMap::new();
        for (key, values) in translations() {
            if backend.contains(key)
                || key.starts_with("client.")
                || key.starts_with("usage-ui.")
                || key.starts_with("schedules-ui.")
            {
                for value in values {
                    result.insert(value.clone(), key.clone());
                }
            }
        }
        result
    });
    if let Some(key) = sources.get(value) {
        return text(key).to_owned();
    }
    // Preserve the diagnostic argument of the one deferred client error template.
    let key = "client.stop-request-not-confirmed-e";
    for template in &translations()[key] {
        if let Some(prefix) = template.strip_suffix("{e}")
            && let Some(detail) = value.strip_prefix(prefix)
        {
            return text(key).replace("{e}", detail);
        }
    }
    for key in [
        "backend.task-unfinished-detail",
        "backend.task-failed-detail",
    ] {
        for template in &translations()[key] {
            if let Some(prefix) = template.strip_suffix("{0}")
                && let Some(detail) = value.strip_prefix(prefix)
            {
                return interpolate(key, &[&message(detail)]);
            }
        }
    }
    value.to_owned()
}

pub fn phase(value: &str) -> &'static str {
    match value {
        "starting" | "booting" => text("sandbox.phase.starting"),
        "running" => text("sandbox.phase.running"),
        "stopping" => text("sandbox.phase.stopping"),
        "stopped" => text("sandbox.phase.stopped"),
        "failed" => text("sandbox.phase.failed"),
        _ => text("sandbox.phase.unknown"),
    }
}

/// Format numbered parameters in one pass, leaving parameter contents untouched.
fn interpolate(key: &str, arguments: &[&str]) -> String {
    let mut template = text(key);
    let mut result = String::new();
    while let Some(start) = template.find('{') {
        result.push_str(&template[..start]);
        let Some(end) = template[start..].find('}').map(|end| start + end) else {
            result.push_str(&template[start..]);
            return result;
        };
        let parameter = &template[start + 1..end];
        if let Some(value) = parameter
            .parse::<usize>()
            .ok()
            .and_then(|index| arguments.get(index))
        {
            result.push_str(value);
        } else {
            result.push_str(&template[start..=end]);
        }
        template = &template[end + 1..];
    }
    result.push_str(template);
    result
}

/// Localize only the generated report header; the model's report body stays verbatim.
pub fn report(value: &str) -> String {
    let (header, body) = value.split_once('\n').unwrap_or((value, ""));
    if let Some(header) = header.strip_prefix("任务「")
        && let Some((title, status)) = header.rsplit_once('」')
        && matches!(status, "已完成" | "已停止" | "未完成")
    {
        return format!(
            "{}\n{}",
            interpolate("report.task-header", &[title, &message(status)]),
            body
        );
    }
    if let Some(header) = header.strip_prefix("定时计划「")
        && let Some((title, status)) = header.rsplit_once("」：")
    {
        return format!(
            "{}\n{}",
            interpolate("report.schedule-header", &[title, &message(status)]),
            if let Some((answer, id)) = body.rsplit_once("\n计划编号：") {
                format!("{}\n{}", answer, interpolate("report.schedule-id", &[id]))
            } else {
                body.to_owned()
            }
        );
    }
    value.to_owned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn locale_fallback_and_parameters() {
        assert_eq!(rust_i18n::t!("ui.retry", locale = "en"), "Retry");
        assert_eq!(rust_i18n::t!("ui.retry", locale = "zh-CN"), "重试");
        assert_eq!(rust_i18n::t!("ui.retry", locale = "unsupported"), "Retry");
        let one = tr!("schedules-ui.every-minutes", 1);
        let many = tr!("schedules-ui.every-minutes", 2);
        assert_eq!(
            one,
            if is_chinese() {
                "每 1 分钟"
            } else {
                "Every 1 minute"
            }
        );
        assert_eq!(
            many,
            if is_chinese() {
                "每 2 分钟"
            } else {
                "Every 2 minutes"
            }
        );
        assert!(matches_message("client.loading", "Loading…"));
        assert!(matches_message("client.loading", "正在加载…"));
    }

    #[test]
    fn report_preserves_user_title_and_model_body() {
        let original = "任务「User {1} 中文」已完成\nOriginal body 中文 {0}";
        let displayed = report(original);
        assert!(displayed.contains("User {1} 中文"));
        assert!(displayed.ends_with("\nOriginal body 中文 {0}"));
        assert_eq!(report("User message 中文 {0}"), "User message 中文 {0}");
        assert_eq!(message("opaque provider error"), "opaque provider error");
    }
}
