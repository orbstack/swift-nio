package vmtypes

type PHSymlinkRequest struct {
	Src  string `json:"src"`
	Dest string `json:"dest"`
}

type SetupInfo struct {
	AdminSymlinkCommands []PHSymlinkRequest `json:"admin_symlink_commands"`
	AdminMessage         *string            `json:"admin_message,omitempty"`
	AlertProfileChanged  *string            `json:"alert_profile_changed"`
	AlertRequestAddPaths []string           `json:"alert_request_add_paths"`
}

type EnvReport struct {
	Environ []string `json:"environ"`
}

type IDRequest struct {
	ID string `json:"id"`
}

type InternalSetDockerRemoteCtxAddrRequest struct {
	Addr string `json:"addr"`
}
