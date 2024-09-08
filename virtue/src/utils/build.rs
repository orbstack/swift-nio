fn main() {
    println!("cargo::rerun-if-changed=ffi");

    cc::Build::new()
        .file("ffi/access_guard.c")
        .file("ffi/utils/rcu.c")
        .compile("utils");
}
