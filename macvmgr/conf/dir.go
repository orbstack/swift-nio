package conf

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
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

func AppDir() string {
	return ensureDir(HomeDir() + "/." + appid.AppName)
}

func RunDir() string {
	return ensureDir(AppDir() + "/run")
}

func LogDir() string {
	return ensureDir(AppDir() + "/log")
}

func NfsMountpoint() string {
	return ensureDir(HomeDir() + "/" + mounts.NfsDirName)
}

func DataDir() string {
	return ensureDir(AppDir() + "/data")
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

func ExecutableDir() string {
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	return filepath.Dir(ex)
}

func AssetsDir() string {
	return ExecutableDir() + "/../assets/" + buildVariant + "/" + Arch()
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

func NfsSocket() string {
	return RunDir() + "/nfs.sock"
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

func VmgrTimestampFile() string {
	return RunDir() + "/vmgr.version"
}

// in tmpdir to persist even if ~/.orbstack is deleted
// so we can stop it for port fwd
func VmgrLockFile() string {
	return os.TempDir() + "/orbstack-vmgr.lock"
}

// TODO: migrate to /config
func VmConfigFile() string {
	return AppDir() + "/vmconfig.json"
}

func VmStateFile() string {
	return AppDir() + "/vmstate.json"
}

func UserSshDir() string {
	return ensureDir(HomeDir() + "/.ssh")
}

func ExtraSshDir() string {
	return ensureDir(AppDir() + "/ssh")
}

func CliBinDir() string {
	return ExecutableDir() + "/bin"
}

func CliXbinDir() string {
	return ExecutableDir() + "/xbin"
}

func FindXbin(name string) string {
	return CliXbinDir() + "/" + name
}

func UserAppBinDir() string {
	return ensureDir(AppDir() + "/bin")
}

func ShellInitDir() string {
	return ensureDir(AppDir() + "/shell")
}

func DockerCliPluginsDir() string {
	return ensureDir(HomeDir() + "/.docker/cli-plugins")
}

func InstallIDFile() string {
	return AppDir() + "/.installid"
}

func Arch() string {
	// amd64, arm64
	return runtime.GOARCH
}

func UpdatePendingFlag() string {
	return RunDir() + "/.update-pending"
}

func ConfigDir() string {
	return ensureDir(AppDir() + "/config")
}

func DockerDaemonConfig() string {
	return ConfigDir() + "/docker.json"
}
