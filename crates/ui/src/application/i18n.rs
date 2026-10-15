//! Presentation locale; platform preferences are read by the host.
use std::sync::Mutex;
static LOCALE: Mutex<String> = Mutex::new(String::new());
include!(concat!(env!("OUT_DIR"), "/localized_formats.rs"));
pub fn locale() -> String {
    {
        let locale = LOCALE.lock().unwrap();
        if locale.is_empty() {
            "en".into()
        } else {
            locale.clone()
        }
    }
}
pub fn set_locale(locale: &str) -> bool {
    let resolved = crate::localization::resolve(&[locale]);
    let changed = {
        let mut l = LOCALE.lock().unwrap();
        let changed = *l != resolved;
        *l = resolved.into();
        changed
    };
    gpui_kit::component::set_locale(resolved);
    changed
}
pub fn is_chinese() -> bool {
    locale() == "zh-CN"
}
pub fn text(key: &str) -> &'static str {
    crate::localization::Catalog::new(&locale()).text(key)
}
pub fn matches_message(key: &str, value: &str) -> bool {
    crate::localization::Catalog::new(&locale()).matches_message(key, value)
}
pub fn message(value: &str) -> String {
    crate::localization::Catalog::new(&locale()).message(value)
}
pub fn report(value: &str) -> String {
    crate::localization::Catalog::new(&locale()).report(value)
}
