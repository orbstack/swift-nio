package conf

import (
	"os"

	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
)

var (
	hostname string
)

func init() {
	var err error
	hostname, err = os.Hostname()
	if err != nil {
		panic(err)
	}
}

type Config struct {
	DataFsDir     string
	DataFsDevice  string
	SconDataDir   string
	GuestMountSrc string
	HostMountSrc  string
	FakeSrc       string
	HcontrolIP    string
	DNSServer     string
	SSHListenIP4  string
	SSHListenIP6  string
	DockerRootfs  string
	DockerDataDir string
	K8sDataDir    string
	EtcExports    string
	CmdLinksDir   string
	StartNfs      bool
}

var configVM = Config{
	DataFsDir:     "/data",
	DataFsDevice:  "/dev/vdb1",
	SconDataDir:   "/data/scon",
	GuestMountSrc: "/opt/orbstack-guest",
	HostMountSrc:  "/mnt/mac",
	FakeSrc:       "/fake",
	HcontrolIP:    netconf.VnetSecureSvcIP4,
	DNSServer:     netconf.VnetServicesIP4,
	SSHListenIP4:  netconf.VnetGuestIP4,
	SSHListenIP6:  netconf.VnetGuestIP6,
	DockerRootfs:  "/opt/docker-rootfs",
	DockerDataDir: "/data/docker",
	K8sDataDir:    "/data/k8s/default",
	EtcExports:    "/etc/exports",
	CmdLinksDir:   "/data/guest-state/bin/cmdlinks",
	StartNfs:      true,
}

func VM() bool {
	return hostname == "orbhost"
}

func C() *Config {
	if VM() {
		return &configVM
	} else {
		panic("no config")
	}
}
