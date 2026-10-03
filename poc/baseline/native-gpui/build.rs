fn main() {
    println!("cargo:rustc-link-search=native=/tmp/pot-research-20261003");
    println!("cargo:rustc-link-lib=dylib=pot_native");
    println!("cargo:rustc-link-arg=-Wl,-rpath,/tmp/pot-research-20261003");
}
