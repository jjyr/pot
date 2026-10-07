use std::{env, path::PathBuf, process::Command};
fn main() {
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap());
    generate_localization(&out);
    generate_app_metadata(&out);
    let status = Command::new("xcrun")
        .args([
            "swiftc",
            "-swift-version",
            "5",
            "-emit-library",
            "-static",
            "-module-cache-path",
        ])
        .arg(out.join("swift-modules"))
        .arg(out.join("Translations.swift"))
        .arg(out.join("AppMetadata.swift"))
        .arg("swift/About.swift")
        .arg("swift/Localization.swift")
        .arg("swift/Bridge.swift")
        .arg("swift/RuntimeClient.swift")
        .arg("swift/Keychain.swift")
        .arg("swift/LocalRuntime.swift")
        .arg("swift/SettingsPanel.swift")
        .arg("swift/AIConfiguration.swift")
        .arg("swift/APIIntegration.swift")
        .arg("swift/Authorization.swift")
        .arg("swift/UnreadTracker.swift")
        .arg("swift/TokenCount.swift")
        .arg("swift/PotIcon.swift")
        .arg("-o")
        .arg(out.join("libpot_native.a"))
        .status()
        .expect("xcrun swiftc is required");
    assert!(status.success(), "Swift bridge compilation failed");
    println!("cargo:rustc-link-search=native={}", out.display());
    println!("cargo:rustc-link-lib=static=pot_native");
    for framework in ["AppKit", "WebKit", "Foundation", "Security"] {
        println!("cargo:rustc-link-lib=framework={framework}");
    }
    println!("cargo:rustc-link-search=native=/usr/lib/swift");
    println!("cargo:rustc-link-arg=-Wl,-rpath,/usr/lib/swift");
    println!("cargo:rerun-if-changed=swift/Bridge.swift");
    println!("cargo:rerun-if-changed=swift/About.swift");
    println!("cargo:rerun-if-changed=../LICENSE");
    println!("cargo:rerun-if-changed=../Cargo.toml");
    println!("cargo:rerun-if-changed=Cargo.toml");
    println!("cargo:rerun-if-changed=swift/RuntimeClient.swift");
    println!("cargo:rerun-if-changed=swift/Keychain.swift");
    println!("cargo:rerun-if-changed=swift/LocalRuntime.swift");
    println!("cargo:rerun-if-changed=swift/SettingsPanel.swift");
    println!("cargo:rerun-if-changed=swift/AIConfiguration.swift");
    println!("cargo:rerun-if-changed=swift/APIIntegration.swift");
    println!("cargo:rerun-if-changed=swift/Authorization.swift");
    println!("cargo:rerun-if-changed=swift/UnreadTracker.swift");
    println!("cargo:rerun-if-changed=swift/TokenCount.swift");
    println!("cargo:rerun-if-changed=swift/PotIcon.swift");
}

fn generate_localization(out: &std::path::Path) {
    use std::{collections::BTreeMap, fs};
    let read = |locale: &str| -> BTreeMap<String, String> {
        serde_json::from_str(&fs::read_to_string(format!("locales/{locale}.json")).unwrap())
            .unwrap()
    };
    let en = read("en");
    let zh = read("zh-CN");
    assert_eq!(
        en.keys().collect::<Vec<_>>(),
        zh.keys().collect::<Vec<_>>(),
        "translation keys differ"
    );
    let formats: Vec<String> =
        serde_json::from_str(&fs::read_to_string("localization-format-keys.json").unwrap())
            .unwrap();
    let plurals: BTreeMap<String, usize> =
        serde_json::from_str(&fs::read_to_string("localization-plurals.json").unwrap()).unwrap();
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
            macros.push_str(&format!("({key:?}, {pattern} $(,)?) => {{{{ {bindings} if crate::i18n::is_chinese() {{ format!({:?}, {values}) }} else if {{ let singular_count = {}.to_string(); singular_count == \"1\" }} {{ format!({:?}, {values}) }} else {{ format!({:?}, {values}) }} }}}};\n", zh[key], names[*position], en[&format!("{key}.one")], en[key]));
            continue;
        }
        macros.push_str(&format!("({key:?}, $($args:tt)*) => {{ if crate::i18n::is_chinese() {{ format!({:?}, $($args)*) }} else {{ format!({:?}, $($args)*) }} }};\n", zh[key], en[key]));
    }
    macros.push_str("}\npub(crate) use tr;\n");
    fs::write(out.join("localized_formats.rs"), macros).unwrap();
    let mut swift = String::from(
        "enum TranslationResources {\nstatic let values: [String: [String: String]] = [\n",
    );
    for (locale, values) in [("en", &en), ("zh-CN", &zh)] {
        swift.push_str(&format!("{locale:?}: [\n"));
        for (key, value) in values {
            swift.push_str(&format!(
                "{}: {},\n",
                serde_json::to_string(key).unwrap(),
                serde_json::to_string(value).unwrap()
            ));
        }
        swift.push_str("],\n");
    }
    swift.push_str("]\n}\n");
    fs::write(out.join("Translations.swift"), swift).unwrap();
    for path in [
        "locales",
        "localization-format-keys.json",
        "localization-plurals.json",
        "swift/Localization.swift",
    ] {
        println!("cargo:rerun-if-changed={path}");
    }
}

fn generate_app_metadata(out: &std::path::Path) {
    let authors = env::var("CARGO_PKG_AUTHORS").unwrap();
    let developer = authors
        .split(':')
        .map(|author| author.split('<').next().unwrap().trim())
        .collect::<Vec<_>>()
        .join(", ");
    let license = std::fs::read_to_string("../LICENSE").unwrap();
    let copyright = license
        .lines()
        .find(|line| line.starts_with("Copyright"))
        .unwrap()
        .replace("(c)", "©");
    let mut swift = String::from("enum AppMetadata {\n");
    for (name, value) in [
        ("version", env::var("CARGO_PKG_VERSION").unwrap()),
        ("developer", developer),
        ("repository", env::var("CARGO_PKG_REPOSITORY").unwrap()),
        ("copyright", copyright),
    ] {
        swift.push_str(&format!(
            "static let {name} = {}\n",
            serde_json::to_string(&value).unwrap()
        ));
    }
    swift.push_str("}\n");
    std::fs::write(out.join("AppMetadata.swift"), swift).unwrap();
}
