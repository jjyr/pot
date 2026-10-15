#[path = "../localization/build.rs"]
mod localization;
use std::{env, fs, path::PathBuf};
fn main() {
    println!("cargo:rerun-if-changed=../localization/build.rs");
    println!("cargo:rerun-if-changed=../app/localization-format-keys.json");
    println!("cargo:rerun-if-changed=../app/localization-plurals.json");
    generate_formats(&out_path());
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

fn out_path() -> PathBuf {
    PathBuf::from(env::var_os("OUT_DIR").unwrap())
}
fn generate_formats(out: &std::path::Path) {
    use std::{collections::BTreeMap, fs};
    let catalogs = localization::catalogs(std::path::Path::new("../app/locales"));
    let en = &catalogs["en"];
    let formats: Vec<String> =
        serde_json::from_str(&fs::read_to_string("../app/localization-format-keys.json").unwrap())
            .unwrap();
    let plurals: BTreeMap<String, usize> =
        serde_json::from_str(&fs::read_to_string("../app/localization-plurals.json").unwrap())
            .unwrap();
    let mut macros = String::from("macro_rules! tr {\n");
    for key in &formats {
        if let Some(position) = plurals.get(key) {
            let count = en[key].matches('{').count();
            let names: Vec<String> = (0..count).map(|i| format!("value{i}")).collect();
            let pattern = names
                .iter()
                .map(|name| format!("${name}:expr"))
                .collect::<Vec<_>>()
                .join(", ");
            let values = names.join(", ");
            let bindings = names
                .iter()
                .map(|name| format!("let {name} = ${name};"))
                .collect::<Vec<_>>()
                .join(" ");
            macros.push_str(&format!("({key:?}, {pattern} $(,)?) => {{{{ {bindings} let singular_count = {}.to_string(); match &*crate::application::i18n::locale() {{", names[*position]));
            for (locale, values) in &catalogs {
                macros.push_str(&format!("{locale:?} => if singular_count == \"1\" {{ format!({:?}, {values}) }} else {{ format!({:?}, {values}) }},", values[&format!("{key}.one")], values[key], values=names.join(", ")));
            }
            macros.push_str(&format!("_ => if singular_count == \"1\" {{ format!({:?}, {values}) }} else {{ format!({:?}, {values}) }} }} }}}};\n", en[&format!("{key}.one")], en[key]));
            continue;
        }
        macros.push_str(&format!(
            "({key:?}, $($args:tt)*) => {{ match &*crate::application::i18n::locale() {{"
        ));
        for (locale, values) in &catalogs {
            macros.push_str(&format!(
                "{locale:?} => format!({:?}, $($args)*),",
                values[key]
            ));
        }
        macros.push_str(&format!("_ => format!({:?}, $($args)*) }} }};\n", en[key]));
    }
    macros.push_str("}\npub(crate) use tr;\n");
    fs::write(out.join("localized_formats.rs"), macros).unwrap();
}
