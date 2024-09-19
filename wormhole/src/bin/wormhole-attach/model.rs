use std::os::fd::RawFd;

use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    pub init_pid: i32,
    pub wormhole_mount_tree_fd: RawFd,
    pub exit_code_pipe_write_fd: RawFd,
    pub log_fd: RawFd,
    pub drm_token: String,

    pub container_env: Option<Vec<String>>,
    pub container_workdir: Option<String>,

    pub entry_shell_cmd: Option<String>,
}
