fn main() {
    println!("cargo::rerun-if-changed=src/safe_memcpy.c");

    cc::Build::new()
        .file("src/safe_memcpy.c")
        .compile("safe_memcpy");
}
