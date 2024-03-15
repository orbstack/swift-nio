use std::{fs, io::Write, time::{Duration, SystemTime}};

use anyhow::anyhow;
use colored::Colorize;
use clap::{Parser, Subcommand};
use flock::{Flock, FlockGuard};
use model::{WormholeEnv, CURRENT_VERSION};
use programs::read_and_find_program;
use search::SearchQuery;

mod config;
mod model;
mod programs;
mod flock;
mod search;
mod nixc;

const ENV_PATH: &str = "/nix/orb/data/env";
// just use the directory, which is guaranteed to exist on overlayfs
const ENV_LOCK_PATH: &str = ENV_PATH;

// 30 days
// cache.nixos.org retention is supposed to be forever
const AUTO_UPDATE_INTERVAL: Duration = Duration::from_secs(30 * 24 * 60 * 60);

// to avoid escaping strings
const PACKAGE_ALLOWED_CHARS: &str = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_.";

#[derive(Parser)]
#[command(version, about, long_about = None)]
#[command(propagate_version = true)]
struct Cli {
    #[command(subcommand)]
    command: Commands,
}

#[derive(Subcommand)]
enum Commands {
    /// Install package(s)
    #[clap(alias("add"), alias("i"))]
    Install {
        #[arg(required=true, num_args=1..)]
        name: Vec<String>,
    },
    /// Uninstall package(s)
    #[clap(alias("remove"), alias("rm"), alias("del"))]
    Uninstall {
        #[arg(required=true, num_args=1..)]
        name: Vec<String>,
    },
    /// List installed packages
    #[clap(alias("ls"))]
    List,
    /// Update packages + index
    /// TODO: should this be the same?
    #[clap(alias("update"), alias("up"))]
    Upgrade,
    /// Search for packages
    Search {
        query: String,
        #[arg(short, long)]
        program: bool,
    },

    /// Internal command-not-found handler
    #[clap(hide=true)]
    #[command(name="__command_not_found")]
    CommandNotFound {
        name: String,
    },
}

fn read_env() -> anyhow::Result<FlockGuard<WormholeEnv>> {
    let lock = Flock::new_nonblock(ENV_LOCK_PATH)?;
    let env_json = match fs::read_to_string(ENV_PATH.to_string() + "/wormhole.json") {
        Ok(json) => json,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            // initialize:
            // - write default flake
            // - create lock
            // - write default env
            let env = WormholeEnv::default();
            nixc::write_flake(&env)?;
            nixc::update_flake_lock()?;
            write_env(&env)?;
            return Ok(FlockGuard::new(lock, env));
        }
        Err(e) => return Err(e.into()),
    };

    let env: WormholeEnv = serde_json::from_str(&env_json)?;
    if env.version != CURRENT_VERSION {
        return Err(anyhow!("wormhole.json version mismatch (expected {}, got {})", CURRENT_VERSION, env.version));
    }

    Ok(FlockGuard::new(lock, env))
}

fn write_env(env: &WormholeEnv) -> anyhow::Result<()> {
    // atomically write to wormhole.json
    // should only be done after nix operation is done
    let env_json = serde_json::to_string_pretty(&env)?;

    let mut file = fs::OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .open(ENV_PATH.to_string() + "/wormhole.json.tmp")?;
    file.write_all(env_json.as_bytes())?;
    // fsync
    file.sync_all()?;
    drop(file);

    fs::rename(ENV_PATH.to_string() + "/wormhole.json.tmp", ENV_PATH.to_string() + "/wormhole.json")?;

    Ok(())
}

fn cmd_install(attr_paths: &[String]) -> anyhow::Result<()> {
    let mut has_error = false;
    let mut has_success = false;

    let mut env = read_env()?;
    let mut found_pkgs = nixc::resolve_package_names(attr_paths)?;
    for iter_name in attr_paths {
        let mut attr_path = iter_name.clone();

        // make sure pkg isn't already installed
        if env.packages.iter().any(|p| p.attr_path == *attr_path) {
            eprintln!("{}", format!("package '{}' already installed", attr_path).red());
            has_error = true;
            continue;
        }

        // validate package name
        if !attr_path.chars().all(|c| PACKAGE_ALLOWED_CHARS.contains(c)) {
            eprintln!("{}", format!("package name '{}' contains invalid characters", attr_path).red());
            has_error = true;
            continue;
        }

        // make sure package exists
        if !found_pkgs.contains_key(&attr_path) {
            if let Some(new_pkg_name) = programs::read_and_find_program(&attr_path)? {
                println!("{}", format!("using package '{}' to provide '{}'", new_pkg_name, attr_path).dimmed());

                // try again (in case cnf.sqlite doesn't match new nixpkgs)
                attr_path = new_pkg_name;
                found_pkgs.extend(nixc::resolve_package_names(&[attr_path.clone()])?);
            }
        }
        if !found_pkgs.contains_key(&attr_path) {
            eprintln!("{}", format!("package '{}' not found", attr_path).red());
            has_error = true;
            continue;
        }

        // add package to env
        let pkg = model::Package {
            attr_path: attr_path.to_string(),
            symbolic_name: found_pkgs[&attr_path].to_string(),
        };
        env.packages.push(pkg);
        has_success = true;
    }

    if has_success {
        nixc::write_flake(&env)?;
        nixc::build_flake_env()?;
        // commit success
        write_env(&env)?;

        // do we need to do auto-update?
        if env.last_updated_at.elapsed()? > AUTO_UPDATE_INTERVAL {
            do_upgrade(&mut env)?;
        }
    }

    if has_error {
        return Err(anyhow!("failed to install some packages"));
    } else {
        let symbolic_names = found_pkgs.values()
            .map(|name| name.to_string())
            .collect::<Vec<_>>()
            .join(", ");
        println!("{}", format!("installed {} package{}: {}", found_pkgs.len(), if found_pkgs.len() == 1 { "" } else { "s" }, symbolic_names).green());
        Ok(())
    }
}

fn cmd_uninstall(attr_paths: &[String]) -> anyhow::Result<()> {
    let mut has_error = false;
    let mut num_success = 0;

    let mut env = read_env()?;
    let mut uninstalled_names = Vec::new();
    for attr_path in attr_paths {
        // make sure pkg is installed
        if !env.packages.iter().any(|p| p.attr_path == *attr_path) {
            eprintln!("{}", format!("package '{}' not installed", attr_path).red());
            has_error = true;
            continue;
        }

        // remove package from env
        let package = env.packages.iter().find(|p| p.attr_path == *attr_path).unwrap();
        uninstalled_names.push(package.symbolic_name.clone());
        env.packages.retain(|p| p.attr_path != *attr_path);
        num_success += 1;
    }

    if num_success > 0 {
        nixc::write_flake(&env)?;
        nixc::build_flake_env()?;
        // commit success
        write_env(&env)?;

        // no auto-update on uninstall - that's not expected to cause a network fetch

        nixc::gc_store()?;
    }

    if has_error {
        return Err(anyhow!("failed to uninstall some packages"));
    } else {
        let symbolic_names = uninstalled_names.join(", ");
        println!("{}", format!("uninstalled {} package{}: {}", num_success, if num_success == 1 { "" } else { "s" }, symbolic_names).green());
        Ok(())
    }
}

fn cmd_list() -> anyhow::Result<()> {
    let mut env = read_env()?;

    env.packages.sort_by(|a, b| a.attr_path.cmp(&b.attr_path));
    for pkg in &env.packages {
        println!("{}  {}", pkg.attr_path, format!("({})", pkg.symbolic_name).dimmed());
    }

    Ok(())
}

fn do_upgrade(env: &mut WormholeEnv) -> anyhow::Result<()> {
    nixc::update_flake_lock()?;

    nixc::build_flake_env()?;
    nixc::gc_store()?;

    // update last_updated_at
    env.last_updated_at = SystemTime::now();
    write_env(&env)?;

    Ok(())
}

fn cmd_upgrade() -> anyhow::Result<()> {
    // create if first time
    let mut env = read_env()?;
    do_upgrade(&mut env)?;
    Ok(())
}

fn cmd_search(query: &str, by_program: bool) -> anyhow::Result<()> {
    let query = if by_program {
        SearchQuery::Program(query.to_string())
    } else {
        SearchQuery::Name(query.to_string())
    };
    let results = search::search_elastic(query)?;
    search::print_results(results);

    Ok(())
}

fn cmd_cnf(name: &str) -> anyhow::Result<()> {
    eprintln!("{}: command not found", name);

    if let Some(pkg_name) = read_and_find_program(name)? {
        eprint!("  * install package '{}'? [y/N] ", pkg_name);
        let mut input = String::new();
        std::io::stdin().read_line(&mut input)?;
        if input.trim().to_lowercase() == "y" {
            cmd_install(&[pkg_name])?;
            // exit with status code to indicate this
            eprintln!(); // space between installation and new command output
            std::process::exit(126);
        }
    }

    Ok(())
}

fn main() -> anyhow::Result<()> {
    let cli = Cli::parse();

    // You can check for the existence of subcommands, and if found use their
    // matches just as you would the top level cmd
    match &cli.command {
        Commands::Install { name } => {
            cmd_install(&name)?;
        }
        Commands::Uninstall { name } => {
            cmd_uninstall(&name)?;
        }
        Commands::List => {
            cmd_list()?;
        }
        Commands::Upgrade => {
            cmd_upgrade()?;
        }
        Commands::Search { program, query } => {
            cmd_search(query, *program)?;
        }
        Commands::CommandNotFound { name } => {
            cmd_cnf(&name)?;
        }
    }

    Ok(())
}
