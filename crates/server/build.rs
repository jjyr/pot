use std::{env, fs, path::PathBuf};

fn main() {
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap()).join("web_assets.rs");
    let mut source = String::from(
        "fn embedded_asset(name: &str) -> Option<&'static EmbeddedAsset> { match name {\n",
    );
    if env::var_os("CARGO_FEATURE_EMBEDDED_WEB").is_some() {
        let root = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap()).join("web-assets");
        println!("cargo:rerun-if-changed={}", root.display());
        let manifest: serde_json::Value = serde_json::from_slice(
            &fs::read(root.join("manifest.json"))
                .expect("Missing checked-in Web assets; run mise run build:mobile"),
        )
        .unwrap();
        for name in manifest["assets"].as_object().unwrap().keys() {
            let path = root.join(format!("{name}.br"));
            assert!(path.is_file(), "Missing embedded Web asset: {name}");
            source.push_str(&format!(
                "{name:?} => {{ static ASSET: EmbeddedAsset = EmbeddedAsset {{ compressed: include_bytes!({:?}), decoded: std::sync::OnceLock::new() }}; Some(&ASSET) }},\n",
                path.display().to_string()
            ));
        }
    }
    source.push_str("_ => None }}\n");
    fs::write(out, source).unwrap();
}
