use std::{collections::HashMap, fs::{self, File}, io::Write, os::unix::process::CommandExt, process::{Command, ExitStatus, Output}, sync::atomic::{AtomicUsize, Ordering}};

use anyhow::anyhow;
use nix::unistd::getpid;
use wormholefs::flock::Flock;

use crate::{base_img, config, model::{NixFlakeArchive, WormholeEnv}, ENV_PATH};

const NIX_TMPDIR: &str = "/nix/orb/data/tmp";
const NIX_HOME: &str = "/nix/orb/data/home";

const ENV_OUT_PATH: &str = "/nix/orb/data/.env-out";

const NIX_BIN: &str = "/nix/orb/sys/.bin";

pub static NUM_RUNNING_PROCS: AtomicUsize = AtomicUsize::new(0);

struct ProcessGuard;

impl Drop for ProcessGuard {
    fn drop(&mut self) {
        NUM_RUNNING_PROCS.fetch_sub(1, Ordering::Relaxed);
    }
}

fn run_with_output_checked(action: &str, restore_if_failed: bool, cmd: &mut Command) -> anyhow::Result<Output> {
    NUM_RUNNING_PROCS.fetch_add(1, Ordering::Relaxed);
    let _guard = ProcessGuard;

    let output = cmd.output()?;
    if !output.status.success() {
        // try to restore missing paths if failed
        if restore_if_failed {
            if let Err(e) = base_img::restore_missing() {
                eprintln!("failed to restore missing paths: {}", e);
            }

            // also repair nix db
            if let Err(e) = run_with_status_checked("repair nix db", false, new_command("nix-store")
                .args(&["--verify", "--repair", "--quiet"])) {
                eprintln!("failed to repair nix db: {}", e);
            }
        }

        return Err(anyhow!("failed to {} ({}): {}", action, output.status, String::from_utf8_lossy(&output.stderr)));
    }

    Ok(output)
}

fn run_with_status_checked(action: &str, restore_if_failed: bool, cmd: &mut Command) -> anyhow::Result<ExitStatus> {
    NUM_RUNNING_PROCS.fetch_add(1, Ordering::Relaxed);
    let _guard = ProcessGuard;

    let status = cmd.status()?;
    if !status.success() {
        // try to restore missing paths if failed
        if restore_if_failed {
            if let Err(e) = base_img::restore_missing() {
                eprintln!("failed to restore missing paths: {}", e);
            }

            // also repair nix db
            if let Err(e) = run_with_status_checked("repair nix db", false, new_command("nix-store")
                .args(&["--verify", "--repair", "--quiet"])) {
                eprintln!("failed to repair nix db: {}", e);
            }
        }

        return Err(anyhow!("failed to {} ({})", action, status));
    }

    Ok(status)
}

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
    let output: Output = run_with_output_checked("read flake inputs", true, new_command("nix")
        .args(&["flake", "archive", "--json", "--dry-run", "--impure"]))?;

    let flake_archive = serde_json::from_slice::<NixFlakeArchive>(&output.stdout)?;
    let mut inputs = flake_archive.inputs.values()
        .map(|input| input.path.clone())
        .collect::<Vec<_>>();
    inputs.push(flake_archive.path);

    Ok(inputs)
}

fn new_command(bin: &str) -> Command {
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
                let ret = libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGINT, 0, 0, 0);
                if ret == -1 {
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
    // so abuse package manager temproots mechanism to inject roots WITHOUT isValidPath check
    // https://github.com/NixOS/nix/blob/c152c2767a262b772c912287e1c2d85173b4781c/src/libstore/gc.cc#L197

    // load base image paths
    let mut roots: Vec<String> = base_img::list()?;
    // prepend /nix/store/ to all paths
    for path in &mut roots {
        *path = format!("/nix/store/{}", path);
    }

    // load current flake input paths (nixpkgs source)
    roots.extend(read_flake_inputs()?);

    // add ending null
    roots.push("".to_string());

    // write temporary roots in state dir
    std::fs::create_dir_all("/nix/var/nix/temproots")?;
    let pid = getpid();
    let mut file = File::create(format!("/nix/var/nix/temproots/{}", pid))?;
    // write null-separated paths
    let roots_data = roots.join("\0");
    file.write_all(roots_data.as_bytes())?;
    // hold exclusive flock to make nix think we're still alive
    let _flock = Flock::new_nonblock_legacy_excl(file)?;

    run_with_status_checked("GC store", false, new_command("nix-store")
        .args(&["--gc", "--quiet"]))?;

    // delete file
    std::fs::remove_file(format!("/nix/var/nix/temproots/{}", pid))?;
    Ok(())
}

pub fn build_flake_env() -> anyhow::Result<()> {
    run_with_status_checked("rebuild env", true, new_command("nix")
        .args(&["build", "--impure", "--out-link", ENV_OUT_PATH]))?;

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

    let output = run_with_output_checked("find packages", false, new_command("nix")
        .args(&["eval", "--json", "--impure", &format!("nixpkgs#.legacyPackages.{}", config::CURRENT_PLATFORM), "--apply"])
        .arg(nix_expr))?;

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
    run_with_status_checked("update lock", true, new_command("nix")
        .args(&["flake", "update", "--output-lock-file", "flake.lock", "--impure"]))?;

    Ok(())
}
