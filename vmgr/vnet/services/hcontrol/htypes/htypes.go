package htypes

import "github.com/orbstack/macvirt/vmgr/uitypes"

type SSHAgentSockets struct {
	SshConfig string
	Env       string
	Preferred string
}

type User struct {
	Uid      int
	Gid      int
	Username string
	Name     string
	HomeDir  string
}

type DockerMachineConfig struct {
	DockerDaemonConfig string
	K8sEnable          bool
	K8sExposeServices  bool
}

type InitConfig struct {
}

type DockerExitInfo struct {
	Async     bool
	ExitEvent *uitypes.ExitEvent
}
