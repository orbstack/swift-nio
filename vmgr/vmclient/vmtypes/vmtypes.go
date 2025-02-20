package vmtypes

type VmConfig struct {
	MemoryMiB         uint64 `json:"memory_mib"`
	CPU               int    `json:"cpu"`
	Rosetta           bool   `json:"rosetta"`
	NetworkProxy      string `json:"network_proxy"`
	NetworkBridge     bool   `json:"network_bridge"`
	NetworkHttps      bool   `json:"network.https"`
	MountHideShared   bool   `json:"mount_hide_shared"`
	DataDir           string `json:"data_dir,omitempty"`
	DataAllowBackup   bool   `json:"data_allow_backup"`
	DockerSetContext  bool   `json:"docker.set_context"`
	DockerNodeName    string `json:"docker.node_name"`
	SetupUseAdmin     bool   `json:"setup.use_admin"`
	K8sEnable         bool   `json:"k8s.enable"`
	K8sExposeServices bool   `json:"k8s.expose_services"`
	SSHExposePort     bool   `json:"ssh.expose_port"`
}

type PHSymlinkRequest struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

type SetupInfo struct {
	AdminSymlinkCommands []PHSymlinkRequest `json:"admin_symlink_commands"`
	AdminMessage         *string            `json:"admin_message,omitempty"`
	AlertProfileChanged  bool               `json:"alert_profile_changed"`
	AlertRequestAddPath  bool               `json:"alert_request_add_paths"`
}

type DebugInfo struct {
	HeapProfile []byte
}

type EnvReport struct {
	Environ []string `json:"environ"`
}

type IDRequest struct {
	ID string `json:"id"`
}

type K8sNameRequest struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

type InternalSetDockerRemoteCtxAddrRequest struct {
	Addr string `json:"addr"`
}

type InternalUpdateTokenRequest struct {
	RefreshToken string `json:"refresh_token,omitempty"`
}
