use std::{collections::{HashMap, HashSet}, fs, os::unix::process::CommandExt, process::Command};

use anyhow::anyhow;

use crate::{config, model::{NixFlakeArchive, WormholeEnv}, ENV_PATH};

const NIX_TMPDIR: &str = "/nix/orb/data/tmp";
const NIX_HOME: &str = "/nix/orb/data/home";

const ENV_OUT_PATH: &str = "/nix/orb/data/.env-out";

const NIX_BIN: &str = "/nix/orb/sys/.bin";

pub fn read_flake_inputs() -> anyhow::Result<Vec<String>> {
    // load current flake input paths (nixpkgs source)
    /*
{
  "inputs": {
    "nixpkgs": {
      "inputs": {},
      "path": "/nix/store/ihkdxl68qh2kcsr33z2jhvfdrpcf7xrg-source"
    }
  },
  "path": "/nix/store/zkspxz1kd4wz90lmszaycb1kzx0ff4i5-source"
}
     */
    let output = new_command("nix")
        .args(&["flake", "archive", "--json", "--dry-run", "--impure"])
        .output()?;
    if !output.status.success() {
        return Err(anyhow!("failed to read flake inputs ({}): {}", output.status, String::from_utf8_lossy(&output.stderr)));
    }

    let flake_archive = serde_json::from_slice::<NixFlakeArchive>(&output.stdout)?;
    let mut inputs = flake_archive.inputs.values()
        .map(|input| input.path.clone())
        .collect::<Vec<_>>();
    inputs.push(flake_archive.path);

    Ok(inputs)
}

fn new_command(bin: &str) -> Command {
    //ctrlc::
    let mut cmd = Command::new(format!("{}/{}", NIX_BIN, bin));
    unsafe {
        cmd
            .current_dir(ENV_PATH)
            // allow non-free pkgs (requires passing --impure to commands)
            // note: cache.nixos.org doesn't have these pkgs cached
            .env("NIXPKGS_ALLOW_UNFREE", "1")
            // allow insecure (e.g. python2)
            .env("NIXPKGS_ALLOW_INSECURE", "1")
            // nix creates ~/.nix-profile symlink without this
            .env("HOME", NIX_HOME)
            // and extracts stuff in /tmp
            .env("TMPDIR", NIX_TMPDIR)
            // tie child lifetime to dctl
            // processes are spawned by main thread, so it's safe
            // but use SIGINT, not SIGKILL, for safety
            .pre_exec(|| {
                let ret = libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGINT);
                if ret != 0 {
                    Err(std::io::Error::last_os_error())
                } else {
                    Ok(())
                }
            });
    }
    cmd
}

pub fn gc_store() -> anyhow::Result<()> {
    // we don't ship a nix.db that includes base image paths, and symlinking them into gcroots gets ignored because nix-store checks whether paths are in db's valid paths table
    // so use --print-dead to get a list of paths that it *wants* to delete, and filter out the base image paths
    let output = new_command("nix-store")
        .args(&["--gc", "--print-dead", "--quiet"])
        .output()?;
    if !output.status.success() {
        return Err(anyhow!("failed to enumerate store ({}): {}", output.status, String::from_utf8_lossy(&output.stderr)));
    }

    // load base image paths
    let base_paths_data = fs::read_to_string("/nix/orb/sys/.base.list")?;
    let base_paths = base_paths_data
        .lines()
        .collect::<HashSet<_>>();

    // load current flake input paths (nixpkgs source)
    let flake_inputs = read_flake_inputs()?;

    let stdout = String::from_utf8_lossy(&output.stdout);
    let paths = stdout
        .lines()
        // skip paths that are in the base image paths, or are in flake inputs
        // list only contains last path component
        .filter(|path| !base_paths.contains(path.split('/').last().unwrap()) && !flake_inputs.contains(&path.to_string()))
        .collect::<Vec<_>>();

    // pass non-base paths to nix-store --delete
    // TODO: use --stdin to avoid too many args
    // (nix command "nix store delete" fetches from flake registry for some reason?)
    let status = new_command("nix-store")
        .args(&["--delete", "--quiet"])
        .args(&paths)
        .status()?;
    if !status.success() {
        return Err(anyhow!("failed to delete from store ({})", status));
    }

    Ok(())
}

pub fn build_flake_env() -> anyhow::Result<()> {
    let status = new_command("nix")
        .args(&["build", "--impure", "--out-link", ENV_OUT_PATH])
        .status()?;
    if !status.success() {
        return Err(anyhow!("failed to rebuild environment ({})", status));
    }

    Ok(())
}

pub fn write_flake(env: &WormholeEnv) -> anyhow::Result<()> {
    // generate pkglist
    let pkg_list = env.packages
        .iter()
        .map(|pkg| pkg.attr_path.clone())
        .collect::<Vec<_>>()
        .join(" ");

    // generate flake.nix:
    // format! requires too much escaping
    // TODO: could use builtins.fromJSON (builtins.readFile "wormhole.json")
    let data = r#"
{
  inputs = {
    nixpkgs.url = "flake:nixpkgs";
  };

  outputs = { self, nixpkgs }: let
    supportedSystems = [ "x86_64-linux" "aarch64-linux" ];
    forEachSupportedSystem = f: nixpkgs.lib.genAttrs supportedSystems (system: f {
      pkgs = import nixpkgs { inherit system; };
      system = system;
    });
  in {
    packages = forEachSupportedSystem ({ pkgs, system }: {
      default = pkgs.buildEnv {
          name = "wormhole-env";
          paths = with pkgs; [ PKGLIST ];
          pathsToLink = [ "/" ];
        };
    });
  };
}
    "#.replace("PKGLIST", &pkg_list);
    fs::write(ENV_PATH.to_string() + "/flake.nix", data)?;

    Ok(())
}

// maps to symbolic name (incl. version)
pub fn resolve_package_names(attr_paths: &[String]) -> anyhow::Result<HashMap<String, String>> {
    // takes ~150 ms
    // O(1) wrt. number of packages

    // saves 30+ ms per package to use .name (guaranteed to exist on derivations)
    // evaluating store path requires evaluating inputs, which is slow
    // and takes care of cases like "dctl install python3.name" -- string won't have a name
    //_flake: [ (_flake.neovim or null).name or null (_flake.htop or null).name or null (_flake.python3.version or null).name or null (_flake.neovasdim or null).name or null ]
    let nix_expr_pkglist = attr_paths.iter()
        .map(|name| format!("(_flake.{} or null).name or null", name))
        .collect::<Vec<_>>()
        .join(" ");
    let nix_expr = format!("_flake: [ {} ]", nix_expr_pkglist);

    let output = new_command("nix")
        .args(&["eval", "--json", "--impure", &format!("nixpkgs#.legacyPackages.{}", config::CURRENT_PLATFORM), "--apply"])
        .arg(nix_expr)
        .output()?;
    if !output.status.success() {
        return Err(anyhow!("failed to find packages ({}): {}", output.status, String::from_utf8_lossy(&output.stderr)));
    }

    // parse json
    let str_json = String::from_utf8_lossy(&output.stdout);
    let pkg_names: Vec<Option<String>> = serde_json::from_str(&str_json)?;

    // - by matching index, map package attribute path to symbolic name
    // - only include non-null
    Ok(pkg_names.into_iter()
        .enumerate()
        .filter_map(|(i, name)| name.map(|name| (attr_paths[i].clone(), name)))
        .collect())
}

pub fn update_flake_lock() -> anyhow::Result<()> {
    // passing --output-lock-file suppresses "warning: creating lock file"
    // nix flake update --output-lock-file flake.lock
    let status = new_command("nix")
        .args(&["flake", "update", "--output-lock-file", "flake.lock", "--impure"])
        .status()?;
    if !status.success() {
        return Err(anyhow!("failed to update lock ({})", status));
    }

    Ok(())
}
