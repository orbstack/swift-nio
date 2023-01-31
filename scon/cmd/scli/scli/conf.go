package scli

import (
	"runtime"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/scon/util"
)

type Config struct {
	RpcURL    string
	SshNet    string
	SshAddr   string
	ControlVM bool
}

var (
	configLinux = Config{
		RpcURL:    "http://" + util.DefaultAddress4().String() + ":" + strconv.Itoa(ports.GuestScon),
		SshNet:    "tcp",
		SshAddr:   "172.30.30.2:2222",
		ControlVM: false,
	}

	configDarwin = Config{
		RpcURL:    "http://",
		SshNet:    "unix",
		SshAddr:   conf.SconSSHSocket(),
		ControlVM: true,
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
