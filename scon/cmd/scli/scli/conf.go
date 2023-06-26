package scli

import (
	"runtime"
	"strconv"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
)

type Config struct {
	RpcNetwork string
	RpcAddr    string
	SshNet     string
	SshAddr    string
	ControlVM  bool
}

var (
	configLinux = Config{
		RpcNetwork: "tcp",
		RpcAddr:    util.DefaultAddress4().String() + ":" + strconv.Itoa(ports.GuestScon),
		SshNet:     "tcp",
		SshAddr:    netconf.GuestIP4 + ":2222",
		ControlVM:  false,
	}

	configDarwin = Config{
		RpcNetwork: "unix",
		RpcAddr:    conf.SconRPCSocket(),
		SshNet:     "unix",
		SshAddr:    conf.SconSSHSocket(),
		ControlVM:  true,
	}
)

func Conf() *Config {
	switch runtime.GOOS {
	case "linux":
		return &configLinux
	case "darwin":
		return &configDarwin
	default:
		panic("unsupported OS")
	}
}
