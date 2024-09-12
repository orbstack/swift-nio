use std::path::Path;

fn main() {
    // Expose `ffi/`'s path as an environment variable.
    println!(
        "cargo::rustc-env=FFI_INCLUDE_DIR={}",
        Path::new("ffi/").canonicalize().unwrap().to_str().unwrap()
    );

    // Link the `ffi/` module into the binary.
    println!("cargo::rerun-if-changed=ffi");
    cc::Build::new()
        .file("ffi/multiplexer.c")
        .compile("sigstack");
}
