fn main() {
    println!("cargo::rerun-if-changed=src");
    cc::Build::new()
        .file("src/multiplexer.c")
        .compile("sigstack");
}
