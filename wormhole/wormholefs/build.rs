#[cfg(target_os = "linux")]
fn compile_bpf() {
    use std::process::Command;

    let cwd = std::env::current_dir().unwrap();
    let cwd = cwd.to_str().unwrap();

    let status = Command::new("clang")
        .args(&["-target", "bpfel", "-mcpu=v3", "-fno-ident", "-O2", "-I/usr/include/aarch64-linux-gnu", "-fdebug-compilation-dir", ".", &format!("-fdebug-prefix-map={}=.", cwd), "-c", "src/bpf/wormholefs_bpf.c", "-g", "-o", "wormholefs_bpf.o"])
        .current_dir(cwd)
        .status().unwrap();
    assert!(status.success());

    // strip DWARF
    let status = Command::new("llvm-strip")
        .args(&["-g", "wormholefs_bpf.o"])
        .current_dir(cwd)
        .status().unwrap();
    assert!(status.success());

    // strip BTF strings
    let status = Command::new("go")
        .args(&["run", "./cmd/btfstrip", &(cwd.to_string() + "/wormholefs_bpf.o")])
        .current_dir("../../scon")
        .status().unwrap();
    assert!(status.success());
}

#[cfg(not(target_os = "linux"))]
fn compile_bpf() {}

fn main() {
    println!("cargo:rerun-if-changed=src/bpf/wormholefs_bpf.c");
    compile_bpf();
}
