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

		// go
		"GOBIN",
		"GOCACHE",
		"GOENV",
		"GOMODCACHE",
		"GOEXE",
		"GOMODCACHE",
		"GOOS",
		"GOPATH",
		"GOROOT",
		"GOTMPDIR",
		"GOTOOLDIR",
		"GOMOD",
		"GOWORK",

		// nvm
		"NVM_DIR",
	}

	// need url host translation
	ProxyEnvs = []string{
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"FTP_PROXY",
		"ALL_PROXY",

		// apparently these can be lowercase?
		"http_proxy",
		"https_proxy",
		"ftp_proxy",
		"all_proxy",
	}
)
