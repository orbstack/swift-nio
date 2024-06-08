use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize, Debug, Clone)]
pub struct WormholeConfig {
    pub init_pid: i32,
    pub wormhole_mount_tree_fd: i32,
    pub drm_token: String,

    pub container_env: Option<Vec<String>>,
    pub container_workdir: Option<String>,

    pub entry_shell_cmd: Option<String>,
}
