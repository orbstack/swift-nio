use std::os::fd::RawFd;

use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    // renamed for obfuscation, as this may be user-visible
    #[serde(rename = "a")]
    pub init_pid: i32,
    #[serde(rename = "i")]
    pub rootfs_fd: Option<RawFd>,
    #[serde(rename = "b", default)]
    pub wormhole_mount_tree_fd: RawFd,
    #[serde(rename = "c", default)]
    pub exit_code_pipe_write_fd: RawFd,
    #[serde(rename = "d", default)]
    pub log_fd: RawFd,
    #[serde(rename = "e")]
    pub drm_token: String,

    #[serde(rename = "f")]
    pub container_workdir: Option<String>,
    #[serde(rename = "g")]
    pub container_env: Option<Vec<String>>,

    #[serde(rename = "h")]
    pub entry_shell_cmd: Option<String>,
}
