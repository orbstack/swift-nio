package conf

import (
	"os"

	"github.com/orbstack/macvirt/macvmgr/vnet/netconf"
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
	SconDataDir   string
	GuestMountSrc string
	HostMountSrc  string
	FakeSrc       string
	HcontrolIP    string
	DummyHcontrol bool
	DNSServer     string
	SSHListenIP4  string
	SSHListenIP6  string
	DockerRootfs  string
	DockerDataDir string
	NfsRootRO     string
	NfsRootRW     string
	EtcExports    string
	StartNfs      bool
}

var configVM = Config{
	SconDataDir:   "/data/scon",
	GuestMountSrc: "/opt/orbstack-guest",
	HostMountSrc:  "/mnt/mac",
	FakeSrc:       "/fake",
	HcontrolIP:    netconf.SecureSvcIP4,
	DummyHcontrol: false,
	DNSServer:     netconf.ServicesIP4,
	SSHListenIP4:  netconf.GuestIP4,
	SSHListenIP6:  netconf.GuestIP6,
	DockerRootfs:  "/opt/docker-rootfs",
	DockerDataDir: "/data/docker",
	NfsRootRO:     "/nfsroot-ro",
	NfsRootRW:     "/nfsroot-rw",
	EtcExports:    "/etc/exports",
	StartNfs:      true,
}

var configTest = Config{
	SconDataDir:   "/home/dragon/code/projects/macvirt/scdata",
	GuestMountSrc: "/home/dragon/code/projects/macvirt/rootfs/out/rd/opt/orbstack-guest",
	HostMountSrc:  "/ssdstore",
	FakeSrc:       "/home/dragon/code/projects/macvirt/rootfs/out/rd/fake",
	HcontrolIP:    "127.0.0.1",
	DummyHcontrol: true,
	DNSServer:     "1.1.1.1",
	SSHListenIP4:  "127.0.0.1",
	SSHListenIP6:  "::1",
	DockerRootfs:  "/home/dragon/code/projects/macvirt/rootfs/out/rd/opt/docker-rootfs",
	DockerDataDir: "/home/dragon/code/projects/macvirt/rootfs/out/rd/opt/docker-rootfs/var/lib/docker",
	NfsRootRO:     "/tmp/scon-nfs",
	NfsRootRW:     "/tmp/scon-nfs",
	EtcExports:    "/tmp/scon-nfs/exports",
	StartNfs:      false,
}

func VM() bool {
	return hostname == "orb"
}

func C() *Config {
	if VM() {
		return &configVM
	} else {
		return &configTest
	}
}
