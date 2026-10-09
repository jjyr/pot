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
        .join("web-ui/dist");
    let mut names = [
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
    ]
    .into_iter()
    .map(str::to_owned)
    .collect::<Vec<_>>();
    fn viewer_assets(root: &std::path::Path, dir: &std::path::Path, names: &mut Vec<String>) {
        for entry in fs::read_dir(dir).expect("Run mise run build:mobile to package noVNC") {
            let path = entry.unwrap().path();
            if path.is_dir() {
                viewer_assets(root, &path, names);
            } else if path.extension().is_some_and(|ext| ext == "js") {
                names.push(
                    path.strip_prefix(root)
                        .unwrap()
                        .to_str()
                        .unwrap()
                        .to_owned(),
                );
            }
        }
    }
    viewer_assets(&root, &root.join("desktop-assets"), &mut names);
    let mut source =
        String::from("pub fn embedded_asset(name:&str)->Option<&'static [u8]>{match name {\n");
    for name in names {
        let path = root.join(&name);
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
