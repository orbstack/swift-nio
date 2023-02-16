package sshenv

var (
	NoInheritEnvs = []string{
		"USER",
		"LOGNAME",
		"HOME",
		"PATH",
		"SHELL",
		"TMPDIR",
		"SSH_AUTH_SOCK",
		"ZDOTDIR",
		"LANG", // locale may not be available in linux

		"XDG_SESSION_ID",
		"XDG_RUNTIME_DIR",
		"XPC_SERVICE_NAME",
		"XPC_FLAGS",
		"SECURITYSESSIONID",
	}
)
