use anyhow::anyhow;
use starry::commands;

// we don't use clap because one consumer (docker fast df) runs "du" on up to hundreds of dirs one at a time, so startup time is critical
pub fn main() -> anyhow::Result<()> {
    let args = std::env::args().collect::<Vec<_>>();

    let subcommand = args.get(1).ok_or_else(|| anyhow!("missing subcommand"))?;
    match subcommand.as_str() {
        "cp" => {
            let src_dir = args.get(2).ok_or_else(|| anyhow!("missing src dir"))?;
            let dest_dir = args.get(3).ok_or_else(|| anyhow!("missing dest dir"))?;
            commands::cp::main(src_dir, dest_dir)
        }

        "du" => {
            let src_dirs = &args[2..].iter().map(|s| s.as_str()).collect::<Vec<_>>();
            commands::du::main(src_dirs)
        }

        "find" => {
            let root_dir = args.get(2).ok_or_else(|| anyhow!("missing root dir"))?;
            commands::find::main(root_dir)
        }

        "rm" => {
            let root_dir = args.get(2).ok_or_else(|| anyhow!("missing root dir"))?;
            commands::rm::main(root_dir)
        }

        "tar" => {
            let src_dir = args.get(2).ok_or_else(|| anyhow!("missing src dir"))?;
            commands::tar::main(src_dir)
        }

        "oar" => {
            let src_dir = args.get(2).ok_or_else(|| anyhow!("missing src dir"))?;
            commands::oar::main(src_dir)
        }

        _ => Err(anyhow!("unknown subcommand: {}", subcommand)),
    }
}
