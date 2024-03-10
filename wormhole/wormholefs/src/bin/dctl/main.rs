use std::process::Command;

use colored::Colorize;
use clap::{Parser, Subcommand};
use serde_json::json;
use serde::{Serialize, Deserialize};

const PROFILE_PATH: &str = "/nix/var/nix/profiles/default";
const NIX_BIN: &str = "/nix/orb/.bin";

#[cfg(target_arch = "x86_64")]
const CURRENT_PLATFORM: &str = "x86_64-linux";
#[cfg(target_arch = "aarch64")]
const CURRENT_PLATFORM: &str = "aarch64-linux";

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
    Install { name: Vec<String> },
    /// Uninstall package(s)
    #[clap(alias("remove"), alias("rm"), alias("del"))]
    Uninstall { name: Vec<String> },
    /// List installed packages
    #[clap(alias("ls"))]
    List,
    /// Update package(s)
    #[clap(alias("up"))]
    Upgrade { name: Option<Vec<String>> },
    /// Search for packages
    Search { query: String },
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
    package_attr_name: String,
    package_attr_set: String,
    package_pname: String,
    package_pversion: String,
    package_platforms: Vec<String>,
    //package_outputs: Vec<String>,
    //package_default_output: String,
    package_programs: Vec<String>,
    //package_license: Vec<String>,
    package_license_set: Vec<String>,
    // not String
    //package_maintainers: Vec<String>,
    //package_maintainers_set: Vec<String>,
    package_description: String,
    //package_longDescription: Option<String>,
    //package_hydra: Option<String>,
    package_system: String,
    //package_homepage: Vec<String>,
}

fn search_by_name(query: &str) -> anyhow::Result<Vec<ElasticSearchSource>> {
    let client = reqwest::blocking::Client::new();
    let resp = client.post("https://search.nixos.org/backend/latest-*-nixos-unstable/_search")
        .basic_auth("aWVSALXpZv", Some("X8gPHnzL52wFEekuxsfQ9cSh"))
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
            "query": {
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
                                            "query": query,
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
                                                "value": format!("*{query}*"),
                                                "case_insensitive": true
                                            }
                                        }
                                    },
                                ]
                            }
                        }
                    ]
                }
            }
        }))
        .send()?;

    let body = resp.json::<ElasticSearchResponse>()?;
    Ok(body.hits.hits
        .into_iter()
        .map(|hit| hit._source)
        .collect())
}

fn search_by_program(query: &str) -> anyhow::Result<Option<ElasticSearchSource>> {
    let client = reqwest::blocking::Client::new();
    let resp = client.post("https://search.nixos.org/backend/latest-*-nixos-unstable/_search")
        .basic_auth("aWVSALXpZv", Some("X8gPHnzL52wFEekuxsfQ9cSh"))
        .json(&json!({
            "from": 0,
            "size": 1,
            "sort": [
                {
                    "_score": "desc",
                    "package_attr_name": "desc",
                    "package_pversion": "desc"
                }
            ],
            "query": {
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
                                            "package_programs": query,
                                        }
                                    },
                                ]
                            }
                        }
                    ]
                }
            }
        }))
        .send()?;

    let body = resp.json::<ElasticSearchResponse>()?;
    Ok(body.hits.hits
        .into_iter()
        .map(|hit| hit._source)
        .next())
}

fn print_search_results(results: Vec<ElasticSearchSource>) {
    let len = results.len();
    for (i, source) in results.into_iter().enumerate() {
        // must be available for current platform
        if !source.package_platforms.contains(&CURRENT_PLATFORM.to_string()) {
            continue;
        }

        println!("{}  {}", source.package_attr_name.bold(), source.package_pversion.dimmed());
        println!("  {}", source.package_description);
        if i < len - 1 {
            println!("");
        }
    }
}

fn gc_nix_store() -> anyhow::Result<()> {
    Command::new(NIX_BIN.to_string() + "/nix-store")
        .args(&["gc", "--print-roots"])
        .status()?;

    Ok(())
}

fn cmd_install(name: &[String]) -> anyhow::Result<()> {
    Command::new(NIX_BIN.to_string() + "/nix")
        .args(&["profile", "install", "--profile", PROFILE_PATH, "--impure"])
        // prepend "nixpkgs#" to each
        .args(&name.iter()
            .map(|name| format!("nixpkgs#{}", name))
            .collect::<Vec<String>>())
        .status()?;

    Ok(())
}

fn cmd_uninstall(name: &[String]) -> anyhow::Result<()> {
    Command::new(NIX_BIN.to_string() + "/nix")
        .args(&["profile", "remove", "--profile", PROFILE_PATH, "--impure"])
        // prepend ".*\." to each
        .args(&name.iter()
            .map(|name| format!(".*\\.{}", name))
            .collect::<Vec<String>>())
        .status()?;
    gc_nix_store()?;

    Ok(())
}

fn cmd_list() -> anyhow::Result<()> {
    
    Ok(())
}

fn cmd_upgrade(name: Option<Vec<String>>) -> anyhow::Result<()> {
    let name = name.unwrap_or_default();

    Command::new(NIX_BIN.to_string() + "/nix")
        .args(&["profile", "upgrade", "--profile", PROFILE_PATH, "--impure"])
        // prepend "nixpkgs#" to each
        .args(&name.iter()
            .map(|name| format!("nixpkgs#{}", name))
            .collect::<Vec<String>>())
        .status()?;
    gc_nix_store()?;

    Ok(())
}

fn cmd_search(query: &str) -> anyhow::Result<()> {
    let results = search_by_name(query)?;
    print_search_results(results);

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
        Commands::Upgrade { name } => {
            cmd_upgrade(name.clone())?;
        }
        Commands::Search { query } => {
            cmd_search(query)?;
        }
    }

    Ok(())
}
