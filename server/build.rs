use std::{env, fs, path::PathBuf};
fn main() {
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap()).join("web_assets.rs");
    if env::var_os("CARGO_FEATURE_EMBEDDED_WEB").is_none() {
        fs::write(
            out,
            "pub fn embedded_asset(_: &str)->Option<&'static [u8]>{None}\n",
        )
        .unwrap();
        return;
    }
    let root = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap())
        .parent()
        .unwrap()
        .join("mobile/dist");
    let names = [
        "index.html",
        "locales.json",
        "OFL.txt",
        "sw.js",
        "manifest.json",
        "icon.png",
        "icon-192.png",
        "icon-512.png",
        "install.js",
        "auth.js",
        "style.css",
        "pkg/pot_mobile.js",
        "pkg/pot_mobile_bg.wasm",
        "pkg/pot_mobile.js.gz",
        "pkg/pot_mobile_bg.wasm.gz",
        "font.ttf",
    ];
    let mut source =
        String::from("pub fn embedded_asset(name:&str)->Option<&'static [u8]>{match name {\n");
    for name in names {
        let path = root.join(name);
        assert!(
            path.is_file(),
            "Run mise run build:mobile before building embedded-web"
        );
        println!("cargo:rerun-if-changed={}", path.display());
        source.push_str(&format!(
            "{name:?}=>Some(include_bytes!({:?})),\n",
            path.display().to_string()
        ));
    }
    source.push_str("_=>None}}\n");
    fs::write(out, source).unwrap();
}
