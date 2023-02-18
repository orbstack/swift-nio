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

		// locale may not be available in linux
		"LANG",
		"LC_ALL",
		"LC_CTYPE",
		"LC_COLLATE",
		"LC_MESSAGES",
		"LC_MONETARY",
		"LC_NUMERIC",
		"LC_TIME",

		// linux system
		"XDG_SESSION_ID",
		"XDG_RUNTIME_DIR",

		// mac system
		"XPC_SERVICE_NAME",
		"XPC_FLAGS",
		"SECURITYSESSIONID",
	}
)
