package conf

import "os"

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
	GuestMountSrc string
	HostMountSrc  string
	FakeSrc       string
	HcontrolIP    string
	DummyHcontrol bool
	DNSServer     string
}

var configVM = Config{
	// /mnt/guest-tools?
	GuestMountSrc: "/opt/macvirt-guest",
	HostMountSrc:  "/mnt/mac",
	FakeSrc:       "/fake",
	HcontrolIP:    "172.30.30.201",
	DummyHcontrol: false,
	DNSServer:     "172.30.30.200",
}

var configTest = Config{
	GuestMountSrc: "/home/dragon/code/projects/macvirt/rootfs/out/rd/opt/macvirt-guest",
	HostMountSrc:  "/ssdstore",
	FakeSrc:       "/home/dragon/code/projects/macvirt/rootfs/out/rd/fake",
	HcontrolIP:    "127.0.0.1",
	DummyHcontrol: true,
	DNSServer:     "1.1.1.1",
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
