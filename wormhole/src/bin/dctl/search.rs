use colored::Colorize;
use serde::{Deserialize, Serialize};
use serde_json::json;

use crate::config::CURRENT_PLATFORM;

// index version varies (42 as of writing) but * works
const ELASTICSEARCH_URL: &str = "https://search.nixos.org/backend/latest-*-nixos-unstable/_search";
const ELASTICSEARCH_USERNAME: &str = "aWVSALXpZv";
const ELASTICSEARCH_PASSWORD: &str = "X8gPHnzL52wFEekuxsfQ9cSh";

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
pub struct ElasticSearchSource {
    // to minimize risk of null/type breakage, parse as few fields as possible
    pub package_attr_name: String,
    //pub package_attr_set: String,
    //pub package_pname: String,
    pub package_pversion: String,
    pub package_platforms: Vec<String>,
    //pub package_outputs: Vec<String>,
    //pub package_default_output: String,
    //pub package_programs: Vec<String>,
    //pub package_license: Vec<String>,
    //pub package_license_set: Vec<String>,
    // not String
    //pub package_maintainers: Vec<String>,
    //pub package_maintainers_set: Vec<String>,
    pub package_description: Option<String>,
    //pub package_longDescription: Option<String>,
    //pub package_hydra: Option<String>,
    //pub package_system: String,
    //pub package_homepage: Vec<String>,
}

pub enum SearchQuery {
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

pub fn search_elastic(query: SearchQuery) -> anyhow::Result<Vec<ElasticSearchSource>> {
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

pub fn print_results(results: Vec<ElasticSearchSource>) {
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
