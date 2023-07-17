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
	NfsRootRO     string
	NfsRootRW     string
	EtcExports    string
	CmdLinksDir   string
	StartNfs      bool
}

var configVM = Config{
	DataFsDir:     "/data",
	SconDataDir:   "/data/scon",
	GuestMountSrc: "/opt/orbstack-guest",
	HostMountSrc:  "/mnt/mac",
	FakeSrc:       "/fake",
	HcontrolIP:    netconf.SecureSvcIP4,
	DNSServer:     netconf.ServicesIP4,
	SSHListenIP4:  netconf.GuestIP4,
	SSHListenIP6:  netconf.GuestIP6,
	DockerRootfs:  "/opt/docker-rootfs",
	DockerDataDir: "/data/docker",
	NfsRootRO:     "/nfsroot-ro",
	NfsRootRW:     "/nfsroot-rw",
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
