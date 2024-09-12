fn main() {
    println!("cargo::rerun-if-changed=ffi");
    cc::Build::new()
        .file("ffi/multiplexer.c")
        .compile("sigstack");
}
