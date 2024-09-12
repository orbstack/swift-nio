fn main() {
    println!("cargo::rerun-if-changed=ffi");

    cc::Build::new()
        .include(sigstack::FFI_INCLUDE_DIR)
        .file("ffi/access_guard.c")
        .file("ffi/utils/rcu.c")
        .compile("utils");
}
