package htypes

import (
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
)

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
	DockerNodeName     string
	K8sEnable          bool
	K8sExposeServices  bool
}

type InitConfig struct {
	VmConfig *vmtypes.VmConfig
	Netconf  *netconf.Config
}

type DockerExitInfo struct {
	Async     bool
	ExitEvent *uitypes.ExitEvent
}

type KeychainTLSData struct {
	CertPEM string
	KeyPEM  string
}

type TLSCertificate struct {
	Certificate   [][]byte
	PrivateKeyDER []byte
}
