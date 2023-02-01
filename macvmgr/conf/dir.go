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

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func HomeDir() string {
	home, err := os.UserHomeDir()
	check(err)
	return home
}

func ensureDir(dir string) string {
	err := os.MkdirAll(dir, 0755)
	check(err)
	return dir
}

func ConfigDir() string {
	return ensureDir(HomeDir() + "/." + appid.AppName)
}

func RunDir() string {
	return ensureDir(ConfigDir() + "/run")
}

func LogDir() string {
	return ensureDir(ConfigDir() + "/log")
}

func NfsMountpoint() string {
	return ensureDir(HomeDir() + "/" + nfsDirName)
}

func DataDir() string {
	return ensureDir(ConfigDir() + "/data")
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

func VmStateFile() string {
	return ConfigDir() + "/vmstate.json"
}

func UserSshDir() string {
	return ensureDir(HomeDir() + "/.ssh")
}

func ExtraSshDir() string {
	return ensureDir(ConfigDir() + "/ssh")
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
