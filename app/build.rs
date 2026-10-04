use std::{env, path::PathBuf, process::Command};
fn main() {
    let out = PathBuf::from(env::var_os("OUT_DIR").unwrap());
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
        .arg("swift/Bridge.swift")
        .arg("swift/LocalRuntime.swift")
        .arg("swift/SettingsPanel.swift")
        .arg("swift/UnreadTracker.swift")
        .arg("swift/TokenCount.swift")
        .arg("-o")
        .arg(out.join("libpot_native.a"))
        .status()
        .expect("xcrun swiftc is required");
    assert!(status.success(), "Swift bridge compilation failed");
    println!("cargo:rustc-link-search=native={}", out.display());
    println!("cargo:rustc-link-lib=static=pot_native");
    for framework in ["AppKit", "WebKit", "Foundation"] {
        println!("cargo:rustc-link-lib=framework={framework}");
    }
    println!("cargo:rustc-link-search=native=/usr/lib/swift");
    println!("cargo:rustc-link-arg=-Wl,-rpath,/usr/lib/swift");
    println!("cargo:rerun-if-changed=swift/Bridge.swift");
    println!("cargo:rerun-if-changed=swift/LocalRuntime.swift");
    println!("cargo:rerun-if-changed=swift/SettingsPanel.swift");
    println!("cargo:rerun-if-changed=swift/UnreadTracker.swift");
    println!("cargo:rerun-if-changed=swift/TokenCount.swift");
}
