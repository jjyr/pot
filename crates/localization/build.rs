//! Shared catalog discovery for native and WASM builds.
use std::{collections::BTreeMap, fs, path::Path};
pub fn catalogs(directory: &Path) -> BTreeMap<String, BTreeMap<String, String>> {
    println!("cargo:rerun-if-changed={}", directory.display());
    let mut catalogs = BTreeMap::new();
    for entry in fs::read_dir(directory).unwrap() {
        let path = entry.unwrap().path();
        if path.extension().is_some_and(|value| value == "json") {
            let locale = path.file_stem().unwrap().to_str().unwrap().to_owned();
            let values: BTreeMap<String, String> =
                serde_json::from_str(&fs::read_to_string(path).unwrap()).unwrap();
            assert!(
                values.contains_key("language.name"),
                "Missing language name: {locale}"
            );
            catalogs.insert(locale, values);
        }
    }
    let english = &catalogs["en"];
    for (locale, values) in &catalogs {
        assert!(
            values.keys().eq(english.keys()),
            "Translation keys differ: {locale}"
        );
    }
    catalogs
}
