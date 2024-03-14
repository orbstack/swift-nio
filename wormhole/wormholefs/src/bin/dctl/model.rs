use std::{collections::HashMap, time::SystemTime};

use serde::{Deserialize, Serialize};

pub const CURRENT_VERSION: i32 = 1;

// TODO: manipulate flake.nix directly
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct WormholeEnv {
    pub version: i32,
    pub last_updated_at: SystemTime,
    pub packages: Vec<Package>,
}

// default
impl Default for WormholeEnv {
    fn default() -> Self {
        WormholeEnv {
            version: CURRENT_VERSION,
            last_updated_at: SystemTime::now(),
            packages: Vec::new(),
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Package {
    pub name: String,
}

#[derive(Serialize, Deserialize)]
pub struct NixFlakeArchive {
    pub path: String,
    pub inputs: HashMap<String, NixFlakeArchiveInput>,
}

#[derive(Serialize, Deserialize)]
pub struct NixFlakeArchiveInput {
    pub path: String,
}
