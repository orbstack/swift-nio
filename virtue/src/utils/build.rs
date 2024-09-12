fn main() {
    println!("cargo::rerun-if-changed=ffi");

    cc::Build::new()
        .file("ffi/access_guard.c")
        .file("ffi/utils/rcu.c")
        .include(sigstack::FFI_INCLUDE_DIR)
        .compile("utils");
}
