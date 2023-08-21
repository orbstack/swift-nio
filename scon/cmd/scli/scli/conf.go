package scli

import (
	"runtime"

	"github.com/orbstack/macvirt/vmgr/conf"
)

type Config struct {
	RpcNetwork string
	RpcAddr    string
	SshNet     string
	SshAddr    string
	ControlVM  bool
}

var (
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
	case "darwin":
		return &configDarwin
	default:
		panic("unsupported OS")
	}
}
