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
	}
)
