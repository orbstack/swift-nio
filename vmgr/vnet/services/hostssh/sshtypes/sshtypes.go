package sshtypes

const (
	KeyMeta = "__ORB_CMETA"

	WormholeIDDocker = "_docker"
	WormholeIDHost   = "_ovm"
)

type SshMeta struct {
	Pwd              string
	Argv0            *string
	RawCommand       bool
	PtyStdin         bool
	PtyStdout        bool
	PtyStderr        bool
	WormholeFallback bool
}

type WormholeConfig struct {
	InitPid             int    `json:"init_pid"`
	WormholeMountTreeFd int    `json:"wormhole_mount_tree_fd"`
	DrmToken            string `json:"drm_token"`

	ContainerWorkdir string   `json:"container_workdir,omitempty"`
	ContainerEnv     []string `json:"container_env"`

	EntryShellCmd string `json:"entry_shell_cmd,omitempty"`
}
