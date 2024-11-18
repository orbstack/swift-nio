use std::os::fd::RawFd;

use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    // renamed for obfuscation, as this may be user-visible
    #[serde(rename = "a")]
    pub init_pid: i32,
    #[serde(rename = "b")]
    pub drm_token: String,

    #[serde(rename = "c")]
    pub container_workdir: Option<String>,
    #[serde(rename = "d")]
    pub container_env: Option<Vec<String>>,

    #[serde(rename = "e")]
    pub entry_shell_cmd: Option<String>,
}

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeRuntimeState {
    #[serde(rename = "a")]
    pub rootfs_fd: Option<RawFd>,

    #[serde(rename = "b")]
    pub wormhole_mount_tree_fd: RawFd,
    #[serde(rename = "c")]
    pub exit_code_pipe_write_fd: RawFd,
    #[serde(rename = "d")]
    pub log_fd: RawFd,
}
