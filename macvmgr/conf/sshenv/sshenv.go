package sshenv

var (
	DefaultPassEnvs = []string{
		// pty req includes this, but also send it when piped
		"TERM",

		// terminal (iterm2)
		"TERM_PROGRAM",
		"TERM_PROGRAM_VERSION",
		"TERM_SESSION_ID",
		"COMMAND_MODE",
		"LC_TERMINAL_VERSION",
		"LC_TERMINAL",
		"ITERM_SESSION_ID",
		"ITERM_PROFILE",
		"COLORTERM",

		// default programs depends on PATH
		"TERMINAL",

		// warp
		"LaunchInstanceID",

		// mac
		"__CF_USER_TEXT_ENCODING",
		"__CFBundleIdentifier",

		// ?
		"DISPLAY",

		// default translated ones below
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
