package vmtypes

type SetupInfo struct {
	AdminShellCommand    *string  `json:"admin_shell_command,omitempty"`
	AdminMessage         *string  `json:"admin_message,omitempty"`
	AlertProfileChanged  *string  `json:"alert_profile_changed"`
	AlertRequestAddPaths []string `json:"alert_request_add_paths"`
}

type EnvReport struct {
	Path    string `json:"PATH"`
	Zdotdir string `json:"ZDOTDIR"`
}

type IDRequest struct {
	ID string `json:"id"`
}
