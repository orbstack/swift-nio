package conf

import (
	"os"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
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
	SSHListen     string
	DockerRootfs  string
	NfsRootRO     string
	NfsRootRW     string
}

var configVM = Config{
	SconDataDir: "/data/scon",
	// /mnt/guest-tools?
	GuestMountSrc: "/opt/macvirt-guest",
	HostMountSrc:  "/mnt/mac",
	FakeSrc:       "/fake",
	HcontrolIP:    "172.30.30.201",
	DummyHcontrol: false,
	DNSServer:     "172.30.30.200",
	SSHListen:     "172.30.30.2:" + strconv.Itoa(ports.GuestSconSSH),
	DockerRootfs:  "/opt/docker-rootfs",
	NfsRootRO:     "/nfsroot-ro",
	NfsRootRW:     "/nfsroot-rw",
}

var configTest = Config{
	SconDataDir:   "/home/dragon/code/projects/macvirt/scdata",
	GuestMountSrc: "/home/dragon/code/projects/macvirt/rootfs/out/rd/opt/macvirt-guest",
	HostMountSrc:  "/ssdstore",
	FakeSrc:       "/home/dragon/code/projects/macvirt/rootfs/out/rd/fake",
	HcontrolIP:    "127.0.0.1",
	DummyHcontrol: true,
	DNSServer:     "1.1.1.1",
	SSHListen:     "127.0.0.1:2222",
	DockerRootfs:  "/home/dragon/code/projects/macvirt/rootfs/out/rd/opt/docker-rootfs",
	NfsRootRO:     "/tmp/scon-nfs",
	NfsRootRW:     "/tmp/scon-nfs",
}

func VM() bool {
	return hostname == "vchost"
}

func C() *Config {
	if VM() {
		return &configVM
	} else {
		return &configTest
	}
}
