fn main() {
    println!("cargo::rerun-if-changed=ffi");

    cc::Build::new()
        .include(sigstack::FFI_INCLUDE_DIR)
        .file("ffi/safe_memcpy.c")
        .compile("safe_memcpy");
}
