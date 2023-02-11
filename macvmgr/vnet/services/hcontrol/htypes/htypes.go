package htypes

type SSHAgentSockets struct {
	SshConfig string
	Env       string
	Preferred string
}

type User struct {
	Uid int
	Gid int
	Username string
	Name string
	HomeDir string
}
