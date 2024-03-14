use std::{collections::HashSet, fs, io::Write, process::Command, time::{Duration, SystemTime}};

use anyhow::anyhow;
use colored::Colorize;
use clap::{Parser, Subcommand};
use model::{NixFlakeArchive, WormholeEnv, CURRENT_VERSION};
use programs::read_and_find_program;
use serde_json::json;
use serde::{Serialize, Deserialize};

mod model;
mod programs;

const NIX_TMPDIR: &str = "/nix/orb/data/tmp";
const NIX_HOME: &str = "/nix/orb/data/home";

const ENV_OUT_PATH: &str = "/nix/orb/data/.env-out";
const ENV_PATH: &str = "/nix/orb/data/env";

const NIX_BIN: &str = "/nix/orb/sys/.bin";

// index version varies (42 as of writing) but * works
const ELASTICSEARCH_URL: &str = "https://search.nixos.org/backend/latest-*-nixos-unstable/_search";
const ELASTICSEARCH_USERNAME: &str = "aWVSALXpZv";
const ELASTICSEARCH_PASSWORD: &str = "X8gPHnzL52wFEekuxsfQ9cSh";

#[cfg(target_arch = "x86_64")]
const CURRENT_PLATFORM: &str = "x86_64-linux";
#[cfg(target_arch = "aarch64")]
const CURRENT_PLATFORM: &str = "aarch64-linux";

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

#[derive(Serialize, Deserialize)]
struct ElasticSearchResponse {
    hits: ElasticSearchHits,
}

#[derive(Serialize, Deserialize)]
struct ElasticSearchHits {
    hits: Vec<ElasticSearchHit>,
}

#[derive(Serialize, Deserialize)]
struct ElasticSearchHit {
    _score: f64,
    _source: ElasticSearchSource,
}

#[derive(Serialize, Deserialize)]
struct ElasticSearchSource {
    // to minimize risk of null/type breakage, parse as few fields as possible
    package_attr_name: String,
    //package_attr_set: String,
    //package_pname: String,
    package_pversion: String,
    package_platforms: Vec<String>,
    //package_outputs: Vec<String>,
    //package_default_output: String,
    //package_programs: Vec<String>,
    //package_license: Vec<String>,
    //package_license_set: Vec<String>,
    // not String
    //package_maintainers: Vec<String>,
    //package_maintainers_set: Vec<String>,
    package_description: Option<String>,
    //package_longDescription: Option<String>,
    //package_hydra: Option<String>,
    //package_system: String,
    //package_homepage: Vec<String>,
}

enum SearchQuery {
    Name(String),
    Program(String),
}

impl SearchQuery {
    fn to_json(&self) -> serde_json::Value {
        match self {
            SearchQuery::Name(name) => json!({
                "bool": {
                    "filter": [
                        {
                            "term": {
                                "type": {
                                    "value": "package",
                                    "_name": "filter_packages"
                                }
                            }
                        }
                    ],
                    "must": [
                        {
                            "dis_max": {
                                "tie_breaker": 0.7,
                                "queries": [
                                    {
                                        "multi_match": {
                                            "type": "cross_fields",
                                            "query": name,
                                            "analyzer": "whitespace",
                                            "auto_generate_synonyms_phrase_query": false,
                                            "operator": "and",
                                            "_name": "multi_match_query",
                                            "fields": [
                                                "package_attr_name^9",
                                                "package_attr_name.*^5.3999999999999995",
                                                "package_programs^9",
                                                "package_programs.*^5.3999999999999995",
                                                "package_pname^6",
                                                "package_pname.*^3.5999999999999996",
                                                "package_description^1.3",
                                                "package_description.*^0.78",
                                                "package_longDescription^1",
                                                "package_longDescription.*^0.6",
                                                "flake_name^0.5",
                                                "flake_name.*^0.3"
                                            ]
                                        }
                                    },
                                    {
                                        "wildcard": {
                                            "package_attr_name": {
                                                "value": format!("*{}*", name),
                                                "case_insensitive": true
                                            }
                                        }
                                    }
                                ]
                            }
                        }
                    ]
                }
            }),
            SearchQuery::Program(program) => json!({
                "bool": {
                    "filter": [
                        {
                            "term": {
                                "type": {
                                    "value": "package",
                                    "_name": "filter_packages"
                                }
                            }
                        }
                    ],
                    "must": [
                        {
                            "dis_max": {
                                "tie_breaker": 0.7,
                                "queries": [
                                    {
                                        "match": {
                                            "package_programs": program,
                                        }
                                    },
                                ]
                            }
                        }
                    ]
                }
            }),
        }
    }
}

fn search_elastic(query: SearchQuery) -> anyhow::Result<Vec<ElasticSearchSource>> {
    let client = reqwest::blocking::Client::new();
    let resp = client.post(ELASTICSEARCH_URL)
        .basic_auth(ELASTICSEARCH_USERNAME, Some(ELASTICSEARCH_PASSWORD))
        .json(&json!({
            "from": 0,
            "size": 50,
            "sort": [
                {
                    "_score": "desc",
                    "package_attr_name": "desc",
                    "package_pversion": "desc"
                }
            ],
            "query": query.to_json(),
        }))
        .send()?;

    let body = resp.json::<ElasticSearchResponse>()?;
    Ok(body.hits.hits
        .into_iter()
        .map(|hit| hit._source)
        .collect())
}

fn print_search_results(results: Vec<ElasticSearchSource>) {
    let len = results.len();
    for (i, source) in results.into_iter().rev().enumerate() {
        // must be available for current platform
        if !source.package_platforms.contains(&CURRENT_PLATFORM.to_string()) {
            continue;
        }

        println!("{}  {}", source.package_attr_name.bold(), source.package_pversion.dimmed());
        if let Some(desc) = source.package_description {
            println!("  {}", desc);
        } else {
            println!("  (no description)");
        }
        if i < len - 1 {
            println!("");
        }
    }

    println!("\n{}", "(most relevant result last)".dimmed())
}

fn read_flake_inputs() -> anyhow::Result<Vec<String>> {
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
    let output = new_nix_command("nix")
        .args(&["flake", "archive", "--json", "--dry-run", "--impure"])
        .output()?;
    if !output.status.success() {
        return Err(anyhow!("failed to read flake inputs ({})", output.status));
    }

    let flake_archive = serde_json::from_slice::<NixFlakeArchive>(&output.stdout)?;
    let mut inputs = flake_archive.inputs.values()
        .map(|input| input.path.clone())
        .collect::<Vec<_>>();
    inputs.push(flake_archive.path);

    Ok(inputs)
}

fn new_nix_command(bin: &str) -> Command {
    let mut cmd = Command::new(format!("{}/{}", NIX_BIN, bin));
    cmd
        .current_dir(ENV_PATH)
        // allow non-free pkgs (requires passing --impure to commands)
        // note: cache.nixos.org doesn't have these pkgs cached
        .env("NIXPKGS_ALLOW_UNFREE", "1")
        // allow insecure (e.g. python2)
        .env("NIXPKGS_ALLOW_INSECURE", "1")
        // nix creates ~/.nix-profile symlink without this
        .env("HOME", NIX_HOME)
        .env("TMPDIR", NIX_TMPDIR);
    cmd
}

fn gc_nix_store() -> anyhow::Result<()> {
    // we don't ship a nix.db that includes base image paths, and symlinking them into gcroots gets ignored because nix-store checks whether paths are in db's valid paths table
    // so use --print-dead to get a list of paths that it *wants* to delete, and filter out the base image paths
    let output = new_nix_command("nix-store")
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
    let status = new_nix_command("nix-store")
        .args(&["--delete", "--quiet"])
        .args(&paths)
        .status()?;
    if !status.success() {
        return Err(anyhow!("failed to delete from store ({})", status));
    }

    Ok(())
}

fn build_flake_env() -> anyhow::Result<()> {
    let status = new_nix_command("nix")
        .args(&["build", "--impure", "--out-link", ENV_OUT_PATH])
        .status()?;
    if !status.success() {
        return Err(anyhow!("failed to rebuild environment ({})", status));
    }

    Ok(())
}

fn read_env() -> anyhow::Result<WormholeEnv> {
    let env_json = match fs::read_to_string(ENV_PATH.to_string() + "/wormhole.json") {
        Ok(json) => json,
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
            // initialize:
            // - write default flake
            // - create lock
            // - write default env
            let env = WormholeEnv::default();
            write_flake(&env)?;
            build_flake_env()?;
            write_env(&env)?;
            return Ok(env);
        }
        Err(e) => return Err(e.into()),
    };

    let env: WormholeEnv = serde_json::from_str(&env_json)?;
    if env.version != CURRENT_VERSION {
        return Err(anyhow!("wormhole.json version mismatch (expected {}, got {})", CURRENT_VERSION, env.version));
    }

    Ok(env)
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

fn write_flake(env: &WormholeEnv) -> anyhow::Result<()> {
    // generate pkglist
    let pkg_list = env.packages
        .iter()
        .map(|pkg| pkg.name.clone())
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

fn cmd_install(name: &[String]) -> anyhow::Result<()> {
    let mut has_error = false;
    let mut has_success = false;

    let mut env = read_env()?;
    for iter_name in name {
        let mut pkg_name = iter_name.clone();

        // make sure pkg isn't already installed
        if env.packages.iter().any(|p| p.name == *pkg_name) {
            eprintln!("{}", format!("package '{}' already installed", pkg_name).red());
            has_error = true;
            continue;
        }

        // validate package name
        if !pkg_name.chars().all(|c| PACKAGE_ALLOWED_CHARS.contains(c)) {
            eprintln!("{}", format!("package name '{}' contains invalid characters", pkg_name).red());
            has_error = true;
            continue;
        }

        // make sure package exists
        // TODO: faster way to do this, without 130ms overhead
        let mut output = new_nix_command("nix")
            .args(&["eval", "--json", "--impure"])
            .arg(format!("nixpkgs#{}.version", pkg_name))
            .output()?;
        if !output.status.success() {
            // try searching by program name
            if let Some(new_pkg_name) = programs::read_and_find_program(&pkg_name)? {
                println!("{}", format!("using package '{}' to provide '{}'", new_pkg_name, pkg_name).dimmed());

                // try again
                pkg_name = new_pkg_name;
                output = new_nix_command("nix")
                    .args(&["eval", "--json", "--impure"])
                    // .version is 30 ms faster (160->130ms) but doesn't work for 'neovim'???
                    .arg(format!("nixpkgs#{}", pkg_name))
                    .output()?;
            }
        }
        if !output.status.success() {
            eprintln!("{}", format!("package '{}' not found", pkg_name).red());
            has_error = true;
            continue;
        }

        // add package to env
        let pkg = model::Package {
            name: pkg_name.to_string(),
        };
        env.packages.push(pkg);
        has_success = true;
    }

    if has_success {
        write_flake(&env)?;
        build_flake_env()?;
        // commit success
        write_env(&env)?;

        // do we need to do auto-update?
        if env.last_updated_at.elapsed()? > AUTO_UPDATE_INTERVAL {
            cmd_upgrade()?;
        }
    }

    if has_error {
        return Err(anyhow!("failed to install some packages"));
    } else {
        println!("{}", format!("installed {} packages", name.len()).green());
        Ok(())
    }
}

fn cmd_uninstall(name: &[String]) -> anyhow::Result<()> {
    let mut has_error = false;
    let mut has_success = false;

    let mut env = read_env()?;
    for pkg_name in name {
        // make sure pkg is installed
        if !env.packages.iter().any(|p| p.name == *pkg_name) {
            eprintln!("{}", format!("package '{}' not installed", pkg_name).red());
            has_error = true;
            continue;
        }

        // remove package from env
        env.packages.retain(|p| p.name != *pkg_name);
        has_success = true;
    }

    if has_success {
        write_flake(&env)?;
        build_flake_env()?;
        // commit success
        write_env(&env)?;

        // no auto-update on uninstall - that's not expected to cause a network fetch

        gc_nix_store()?;
    }

    if has_error {
        return Err(anyhow!("failed to uninstall some packages"));
    } else {
        println!("{}", format!("uninstalled {} packages", name.len()).green());
        Ok(())
    }
}

fn cmd_list() -> anyhow::Result<()> {
    let env = read_env()?;

    for pkg in &env.packages {
        println!("{}", pkg.name);
    }

    Ok(())
}

fn cmd_upgrade() -> anyhow::Result<()> {
    // create if first time
    let mut env = read_env()?;

    // nix flake update --commit-lock-file
    let status = new_nix_command("nix")
        .args(&["flake", "update", "--commit-lock-file", "--impure"])
        .status()?;
    if !status.success() {
        return Err(anyhow!("failed to update lock ({})", status));
    }

    build_flake_env()?;
    gc_nix_store()?;

    // update last_updated_at
    env.last_updated_at = SystemTime::now();
    write_env(&env)?;

    Ok(())
}

fn cmd_search(query: &str, by_program: bool) -> anyhow::Result<()> {
    let query = if by_program {
        SearchQuery::Program(query.to_string())
    } else {
        SearchQuery::Name(query.to_string())
    };
    let results = search_elastic(query)?;
    print_search_results(results);

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
