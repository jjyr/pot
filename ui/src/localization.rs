//! Locale-explicit shared catalogs. A phone never changes the Mac preference.
use std::{collections::HashMap, sync::OnceLock};
#[derive(Clone, Copy)]
pub struct Catalog {
    chinese: bool,
}
fn translations() -> &'static HashMap<String, [String; 2]> {
    static TRANSLATIONS: OnceLock<HashMap<String, [String; 2]>> = OnceLock::new();
    TRANSLATIONS.get_or_init(|| {
        let en: HashMap<String, String> =
            serde_json::from_str(include_str!("../../app/locales/en.json"))
                .expect("English catalog");
        let zh: HashMap<String, String> =
            serde_json::from_str(include_str!("../../app/locales/zh-CN.json"))
                .expect("Chinese catalog");
        en.into_iter()
            .map(|(key, value)| {
                let chinese = zh.get(&key).cloned().unwrap_or_else(|| value.clone());
                (key, [value, chinese])
            })
            .collect()
    })
}
impl Catalog {
    pub fn new(locale: &str) -> Self {
        Self {
            chinese: locale == "zh-CN",
        }
    }

    pub fn text(&self, key: &str) -> &'static str {
        translations()
            .get(key)
            .map(|v| v[usize::from(self.chinese)].as_str())
            .unwrap_or("Missing translation")
    }

    pub fn matches_message(&self, key: &str, value: &str) -> bool {
        translations()
            .get(key)
            .is_some_and(|values| values.iter().any(|translation| translation == value))
    }

    /// Translate recognized application notices only; user/model content never calls this.
    pub fn message(&self, value: &str) -> String {
        if value.is_empty() {
            return String::new();
        }
        static SOURCES: OnceLock<HashMap<String, String>> = OnceLock::new();
        let sources = SOURCES.get_or_init(|| {
            let backend: Vec<String> =
                serde_json::from_str(include_str!("../../app/localization-message-keys.json"))
                    .unwrap();
            let mut result = HashMap::new();
            for (key, values) in translations() {
                if backend.contains(key)
                    || key.starts_with("client.")
                    || key.starts_with("usage-ui.")
                    || key.starts_with("schedules-ui.")
                    || key.starts_with("mobile.shell.")
                {
                    for value in values {
                        result.insert(value.clone(), key.clone());
                    }
                }
            }
            result
        });
        if let Some(key) = sources.get(value) {
            return self.text(key).to_owned();
        }
        // Preserve the diagnostic argument of the one deferred client error template.
        let key = "client.stop-request-not-confirmed-e";
        for template in &translations()[key] {
            if let Some(prefix) = template.strip_suffix("{e}")
                && let Some(detail) = value.strip_prefix(prefix)
            {
                return self.text(key).replace("{e}", detail);
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
                    return self.interpolate(key, &[&self.message(detail)]);
                }
            }
        }
        value.to_owned()
    }

    pub fn phase(&self, value: &str) -> &'static str {
        match value {
            "starting" | "booting" => self.text("sandbox.phase.starting"),
            "running" => self.text("sandbox.phase.running"),
            "stopping" => self.text("sandbox.phase.stopping"),
            "stopped" => self.text("sandbox.phase.stopped"),
            "failed" => self.text("sandbox.phase.failed"),
            _ => self.text("sandbox.phase.unknown"),
        }
    }

    /// Format numbered parameters in one pass, leaving parameter contents untouched.
    fn interpolate(&self, key: &str, arguments: &[&str]) -> String {
        let mut template = self.text(key);
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
    pub fn report(&self, value: &str) -> String {
        let (header, body) = value.split_once('\n').unwrap_or((value, ""));
        if let Some(header) = header.strip_prefix("任务「")
            && let Some((title, status)) = header.rsplit_once('」')
            && matches!(status, "已完成" | "已停止" | "未完成")
        {
            return format!(
                "{}\n{}",
                self.interpolate("report.task-header", &[title, &self.message(status)]),
                body
            );
        }
        if let Some(header) = header.strip_prefix("定时计划「")
            && let Some((title, status)) = header.rsplit_once("」：")
        {
            return format!(
                "{}\n{}",
                self.interpolate("report.schedule-header", &[title, &self.message(status)]),
                if let Some((answer, id)) = body.rsplit_once("\n计划编号：") {
                    format!(
                        "{}\n{}",
                        answer,
                        self.interpolate("report.schedule-id", &[id])
                    )
                } else {
                    body.to_owned()
                }
            );
        }
        value.to_owned()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn independent_locales_preserve_literal_model_content_and_report_parameters() {
        let en = Catalog::new("en");
        let zh = Catalog::new("zh-CN");
        assert_eq!(en.text("main.copy-message"), "Copy message");
        assert_ne!(en.text("main.copy-message"), zh.text("main.copy-message"));
        let report = en.report("任务「标题 {0}」已完成\nLiteral body 已完成 {0}");
        assert!(report.contains("标题 {0}"));
        assert!(report.ends_with("Literal body 已完成 {0}"));
        assert_eq!(en.message("Literal unknown {0}"), "Literal unknown {0}");
        assert_eq!(
            zh.message(en.text("mobile.shell.received")),
            zh.text("mobile.shell.received")
        );
        assert_eq!(
            Catalog::new("unsupported").text("main.copy-message"),
            en.text("main.copy-message")
        );
    }
}
