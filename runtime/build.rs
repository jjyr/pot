use std::{env, path::PathBuf, process::Command};
fn main() {
    let root = PathBuf::from(env::var_os("CARGO_MANIFEST_DIR").unwrap())
        .parent()
        .unwrap()
        .to_owned();
    let status = Command::new("python3")
        .arg(root.join("scripts/localize-web"))
        .arg("--output")
        .arg(env::var_os("OUT_DIR").unwrap())
        .status()
        .expect("Python 3 is required to build desktop assets");
    assert!(status.success(), "Desktop asset generation failed");
    for path in [
        "scripts/localize-web",
        "desktop",
        "sandbox-images/linux/default/index.template.html",
        "app/locales",
    ] {
        println!("cargo:rerun-if-changed={}", root.join(path).display());
    }
}
