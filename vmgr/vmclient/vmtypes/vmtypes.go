package vmtypes

type SetupInfo struct {
	AdminShellCommand    *string  `json:"admin_shell_command,omitempty"`
	AdminMessage         *string  `json:"admin_message,omitempty"`
	AlertProfileChanged  *string  `json:"alert_profile_changed"`
	AlertRequestAddPaths []string `json:"alert_request_add_paths"`
}

type EnvReport struct {
	Environ []string `json:"environ"`
}

type IDRequest struct {
	ID string `json:"id"`
}
