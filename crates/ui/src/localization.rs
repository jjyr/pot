//! Locale-explicit shared catalogs. A phone never changes the Mac preference.
use std::{collections::HashMap, sync::OnceLock};
include!(concat!(env!("OUT_DIR"), "/languages.rs"));
#[derive(Clone, Copy)]
pub struct Catalog {
    locale: &'static str,
}
fn translations() -> &'static HashMap<String, HashMap<String, String>> {
    static TRANSLATIONS: OnceLock<HashMap<String, HashMap<String, String>>> = OnceLock::new();
    TRANSLATIONS.get_or_init(|| {
        serde_json::from_str(include_str!(concat!(env!("OUT_DIR"), "/catalogs.json")))
            .expect("Translation catalogs")
    })
}
pub fn resolve(languages: &[&str]) -> &'static str {
    for language in languages {
        let normalized = language.replace('_', "-").to_lowercase();
        if let Some((locale, _)) = LANGUAGES
            .iter()
            .find(|(locale, _)| locale.to_lowercase() == normalized)
            .or_else(|| {
                LANGUAGES.iter().find(|(locale, _)| {
                    locale.to_lowercase().split('-').next() == normalized.split('-').next()
                })
            })
        {
            return locale;
        }
    }
    "en"
}
impl Catalog {
    pub fn locale(&self) -> &'static str {
        self.locale
    }
    pub fn new(locale: &str) -> Self {
        Self {
            locale: resolve(&[locale]),
        }
    }

    pub fn text(&self, key: &str) -> &'static str {
        translations()[self.locale]
            .get(key)
            .or_else(|| translations()["en"].get(key))
            .map(String::as_str)
            .unwrap_or("Missing translation")
    }

    pub fn matches_message(&self, key: &str, value: &str) -> bool {
        translations().values().any(|values| {
            values
                .get(key)
                .is_some_and(|translation| translation == value)
        })
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
            for key in translations()["en"].keys() {
                if backend.contains(key)
                    || key.starts_with("client.")
                    || key.starts_with("usage-ui.")
                    || key.starts_with("schedules-ui.")
                    || key.starts_with("mobile.shell.")
                {
                    for catalog in translations().values() {
                        result.insert(catalog[key].clone(), key.clone());
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
        for catalog in translations().values() {
            let template = &catalog[key];
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
            for catalog in translations().values() {
                let template = &catalog[key];
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
            "unavailable" => self.text("web.unavailable"),
            _ => self.text("sandbox.phase.unknown"),
        }
    }

    /// Format numbered parameters in one pass, leaving parameter contents untouched.
    pub fn format(&self, key: &str, arguments: &[String]) -> String {
        static PLURALS: OnceLock<HashMap<String, usize>> = OnceLock::new();
        let plurals = PLURALS.get_or_init(|| {
            serde_json::from_str(include_str!("../../app/localization-plurals.json"))
                .expect("plural catalog")
        });
        let singular = plurals
            .get(key)
            .and_then(|index| arguments.get(*index))
            .is_some_and(|value| value == "1");
        let selected = if singular {
            format!("{key}.one")
        } else {
            key.to_owned()
        };
        self.interpolate(
            &selected,
            &arguments.iter().map(String::as_str).collect::<Vec<_>>(),
        )
    }

    fn interpolate(&self, key: &str, arguments: &[&str]) -> String {
        let mut template = self.text(key);
        let mut result = String::new();
        let mut sequential = 0;
        while let Some(start) = template.find('{') {
            result.push_str(&template[..start]);
            let Some(end) = template[start..].find('}').map(|end| start + end) else {
                result.push_str(&template[start..]);
                return result;
            };
            let parameter = &template[start + 1..end];
            let (index, specification) = parameter.split_once(':').unwrap_or((parameter, ""));
            let index = if index.is_empty() {
                let index = sequential;
                sequential += 1;
                Some(index)
            } else {
                index.parse::<usize>().ok()
            };
            if let Some(value) = index.and_then(|index| arguments.get(index)) {
                if specification == ".1" {
                    match value.parse::<f64>() {
                        Ok(number) => result.push_str(&format!("{number:.1}")),
                        Err(_) => result.push_str(value),
                    }
                } else {
                    result.push_str(value);
                }
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
    fn language_discovery_matches_exact_tags_then_supported_base() {
        assert_eq!(resolve(&["unsupported", "ZH_hant_HK"]), "zh-CN");
        assert_eq!(resolve(&["en-GB", "zh-CN"]), "en");
        assert_eq!(resolve(&["zhuang", "english"]), "en");
        assert!(LANGUAGES.contains(&("zh-CN", "简体中文")));
    }
    #[test]
    fn page_formats_preserve_literal_arguments_and_native_decimal_precision() {
        let en = Catalog::new("en");
        let zh = Catalog::new("zh-CN");
        assert_eq!(
            en.format("ui.select-sandbox", &["literal {} 中文".into()]),
            "Select Sandbox: literal {} 中文"
        );
        assert_eq!(
            en.format("ui.running-tasks", &["Running".into(), "2".into()]),
            "Running · 2 running tasks"
        );
        assert_eq!(
            en.format(
                "usage-ui.failed-1-seconds-unfinished",
                &["read".into(), "1".into(), "1.234".into(), "0".into()]
            ),
            "read · 1 failed · 1.2 seconds · 0 unfinished"
        );
        assert_eq!(
            en.format(
                "usage-ui.incomplete-records-totals-may-be-incomplete",
                &["1".into()]
            ),
            " · 1 incomplete record; totals may be incomplete"
        );
        assert_eq!(
            en.format(
                "usage-ui.incomplete-records-totals-may-be-incomplete",
                &["2".into()]
            ),
            " · 2 incomplete records; totals may be incomplete"
        );
        assert!(
            !zh.format("main.items-page", &["2".into(), "1".into()])
                .contains("{}")
        );
    }
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
