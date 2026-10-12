use std::{env, fs, path::PathBuf, process::Command};

fn main() {
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap());
    if env::var_os("CARGO_FEATURE_WEB_UI").is_none() {
        fs::write(
            out.join("web_assets.rs"),
            "fn embedded_asset(_: &str) -> Option<&'static EmbeddedAsset> { None }\n",
        )
        .unwrap();
        return;
    }
    let root = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap())
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .to_owned();
    // OUT_DIR is <target>/<profile>/build/<package>/out. A separate target
    // below this shared cache prevents nested Cargo from waiting on our lock.
    let cache = out.ancestors().nth(4).unwrap().join("pot-web");
    println!("cargo:rerun-if-env-changed=PATH");
    let status = Command::new("python3")
        .arg(root.join("scripts/build-mobile"))
        .arg("--cache-dir")
        .arg(&cache)
        .current_dir(&root)
        .status()
        .expect("web-ui requires Python 3, rustup, curl and Brotli CLI (developers: mise install)");
    assert!(
        status.success(),
        "Web asset build failed; see output above. Use --no-default-features to omit web-ui."
    );
    let assets = cache.join("compressed");
    let manifest: serde_json::Value =
        serde_json::from_slice(&fs::read(assets.join("manifest.json")).unwrap()).unwrap();
    for name in manifest["sources"].as_object().unwrap().keys() {
        println!("cargo:rerun-if-changed={}", root.join(name).display());
    }
    // Track directories too so adding/removing a source invalidates the cache.
    for name in [
        "crates/web-ui/src",
        "crates/web-ui/web",
        "crates/web-ui/fonts",
        "crates/ui/src",
        "crates/core/src",
        "crates/protocol/src",
        "crates/localization/src",
        "crates/app/locales",
        "desktop",
    ] {
        if root.join(name).exists() {
            println!("cargo:rerun-if-changed={}", root.join(name).display());
        }
    }
    println!(
        "cargo:rerun-if-changed={}",
        manifest["compressor"].as_str().unwrap()
    );
    println!("cargo:rerun-if-changed={}", assets.display());
    let mut source = String::from(
        "fn embedded_asset(name: &str) -> Option<&'static EmbeddedAsset> { match name {\n",
    );
    for name in manifest["assets"].as_object().unwrap().keys() {
        let path = assets.join(format!("{name}.br"));
        source.push_str(&format!(
            "{name:?} => {{ static ASSET: EmbeddedAsset = EmbeddedAsset {{ compressed: include_bytes!({:?}), decoded: std::sync::OnceLock::new() }}; Some(&ASSET) }},\n",
            path.display().to_string()
        ));
    }
    source.push_str("_ => None }}\n");
    fs::write(out.join("web_assets.rs"), source).unwrap();
}
