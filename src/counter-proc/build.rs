use std::fs;

fn main() {
    eprintln!("cargo::rerun-if-env-changed=CARGO_CFG_COMPILED_COUNTERS");

    fs::write(
        {
            let mut path = std::path::PathBuf::from(std::env::var("OUT_DIR").unwrap());
            path.push("env_compiled_counters.txt");
            path
        },
        std::env::var("CARGO_CFG_COMPILED_COUNTERS").unwrap_or_default(),
    )
    .unwrap();
}
