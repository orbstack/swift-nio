package conf

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
)

const (
	nfsDirName = "Linux"
)

func HomeDir() string {
	home, _ := os.UserHomeDir()
	return home
}

func ConfigDir() string {
	dir := HomeDir() + "/." + appid.AppName
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func RunDir() string {
	dir := ConfigDir() + "/run"
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func LogDir() string {
	dir := ConfigDir() + "/log"
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func NfsMountpoint() string {
	dir := HomeDir() + "/" + nfsDirName
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func DataDir() string {
	dir := ConfigDir() + "/data"
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		panic(err)
	}
	return dir
}

func GetDataFile(name string) string {
	return DataDir() + "/" + name
}

func DataImage() string {
	return GetDataFile("data.img")
}

func SwapImage() string {
	return GetDataFile("swap.img")
}

func AssetsDir() string {
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	exPath := filepath.Dir(ex)
	return exPath + "/../assets/" + buildVariant + "/" + Arch()
}

func GetAssetFile(name string) string {
	return AssetsDir() + "/" + name
}

func DockerSocket() string {
	return RunDir() + "/docker.sock"
}

func SconSSHSocket() string {
	return RunDir() + "/sconssh.sock"
}

func SconRPCSocket() string {
	return RunDir() + "/sconrpc.sock"
}

func VmControlSocket() string {
	return RunDir() + "/vmcontrol.sock"
}

func HostSSHAgentSocket() string {
	return os.Getenv("SSH_AUTH_SOCK")
}

func ConsoleLog() string {
	return LogDir() + "/console.log"
}

func VmManagerLog() string {
	return LogDir() + "/vmgr.log"
}

func VmgrPidFile() string {
	return RunDir() + "/vmgr.pid"
}

func VmConfigFile() string {
	return ConfigDir() + "/vmconfig.json"
}

func Arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x86_64"
	case "arm64":
		return "arm64"
	default:
		panic("unsupported architecture " + runtime.GOARCH)
	}
}
