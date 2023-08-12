package sshenv

var (
	defaultPassEnvKeys = []string{
		// pty req includes this, but also send it when piped
		"TERM",

		// terminal (iterm2)
		"TERM_PROGRAM",
		"TERM_PROGRAM_VERSION",
		"TERM_SESSION_ID",
		"COMMAND_MODE",
		"ITERM_SESSION_ID",
		"ITERM_PROFILE",
		"COLORTERM",

		// match macOS /etc/ssh/ssh_config: LANG, LC_*
		"LANG",
		// LC_* logic is somewhere else

		// default programs depends on PATH
		"TERMINAL",

		// warp
		"LaunchInstanceID",

		// vs code
		// otherwise running wrapper "code" shell script from linux opens cli.js
		"ELECTRON_RUN_AS_NODE",

		// mac
		"__CF_USER_TEXT_ENCODING",
		"__CFBundleIdentifier",

		// ?
		"DISPLAY",

		// need to propagate this for correct translation
		"ORBENV",
		"WSLENV",

		// default translated ones below
	}

	// need url host translation
	proxyEnvKeys = []string{
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
