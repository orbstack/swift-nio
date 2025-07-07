package vmtypes

type VmConfig struct {
	MemoryMiB          uint64 `json:"memory_mib"`
	CPU                int    `json:"cpu"`
	Rosetta            bool   `json:"rosetta"`
	Network_Proxy      string `json:"network_proxy"`
	Network_Bridge     bool   `json:"network_bridge"`
	Network_Https      bool   `json:"network.https"`
	MountHideShared    bool   `json:"mount_hide_shared"`
	DataDir            string `json:"data_dir,omitempty"`
	DataAllowBackup    bool   `json:"data_allow_backup"`
	Docker_SetContext  bool   `json:"docker.set_context"`
	Docker_NodeName    string `json:"docker.node_name"`
	Setup_UseAdmin     bool   `json:"setup.use_admin"`
	K8s_Enable         bool   `json:"k8s.enable"`
	K8s_ExposeServices bool   `json:"k8s.expose_services"`
	SSH_ExposePort     bool   `json:"ssh.expose_port"`
	Power_PauseOnSleep bool   `json:"power.pause_on_sleep"`
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
