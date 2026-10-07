//! Application translations. Static translations are cached once per supported locale.

include!(concat!(env!("OUT_DIR"), "/localized_formats.rs"));

pub fn set_locale(locale: &str) -> bool {
    let locale = pot_ui::localization::resolve(&[locale]);
    if &*rust_i18n::locale() == locale {
        return false;
    }
    gpui_kit::component::set_locale(locale);
    true
}

pub fn is_chinese() -> bool {
    &*rust_i18n::locale() == "zh-CN"
}

pub fn text(key: &str) -> &'static str {
    pot_ui::localization::Catalog::new(&rust_i18n::locale()).text(key)
}
pub fn matches_message(key: &str, value: &str) -> bool {
    pot_ui::localization::Catalog::new(&rust_i18n::locale()).matches_message(key, value)
}
pub fn message(value: &str) -> String {
    pot_ui::localization::Catalog::new(&rust_i18n::locale()).message(value)
}
pub fn report(value: &str) -> String {
    pot_ui::localization::Catalog::new(&rust_i18n::locale()).report(value)
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
