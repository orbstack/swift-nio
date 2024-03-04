fn main() {
    #[cfg(target_os = "macos")]
    println!("cargo:rustc-link-lib=framework=Hypervisor");
    println!("cargo:rustc-link-lib=krunfw.4");
    println!(
        "cargo:rustc-link-search={}/../../res",
        std::env::var("CARGO_MANIFEST_DIR").unwrap(),
    );
}
