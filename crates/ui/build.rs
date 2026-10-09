#[path = "../localization/build.rs"]
mod localization;
use std::{env, fs, path::PathBuf};
fn main() {
    println!("cargo:rerun-if-changed=../localization/build.rs");
    let catalogs = localization::catalogs(&PathBuf::from("../app/locales"));
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap());
    fs::write(
        out.join("catalogs.json"),
        serde_json::to_string(&catalogs).unwrap(),
    )
    .unwrap();
    let mut languages = String::from("pub const LANGUAGES: &[(&str, &str)] = &[\n");
    for (locale, values) in catalogs {
        languages.push_str(&format!("({locale:?}, {:?}),\n", values["language.name"]));
    }
    languages.push_str("];\n");
    fs::write(out.join("languages.rs"), languages).unwrap();
}
