package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/orbstack/macvirt/macvmgr/conf/coredir"
	"github.com/orbstack/macvirt/macvmgr/vmconfig"
)

const (
	VmgrExeName = "OrbStack Helper (VM)"
)

func ensureDir(dir string) string {
	_, err := coredir.EnsureDir(dir)
	if err != nil {
		panic(err)
	}
	return dir
}

func HomeDir() string {
	return coredir.HomeDir()
}

func AppDir() string {
	return coredir.AppDir()
}

func RunDir() string {
	return ensureDir(AppDir() + "/run")
}

func LogDir() string {
	return ensureDir(AppDir() + "/log")
}

func DataDir() string {
	dir := vmconfig.Get().DataDir
	if dir != "" {
		return ensureDir(dir)
	}
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

func ExecutableDir() (string, error) {
	// TODO better debug path
	if Debug() {
		return HomeDir() + "/code/projects/macvirt/cli-bin", nil
	}

	selfExe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get exe path: %w", err)
	}

	// resolve symlinks
	selfExe, err = filepath.EvalSymlinks(selfExe)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}

	// find parent app bundle if we're in a nested bundle
	nestedBundleSuffix := "/Frameworks/" + VmgrExeName + ".app/Contents/MacOS/" + VmgrExeName
	if strings.HasSuffix(selfExe, nestedBundleSuffix) {
		rootBundlePath := strings.TrimSuffix(selfExe, nestedBundleSuffix)
		return rootBundlePath + "/Contents/MacOS", nil
	}

	return filepath.Dir(selfExe), nil
}

func MustExecutableDir() string {
	dir, err := ExecutableDir()
	if err != nil {
		panic(err)
	}
	return dir
}

func AssetsDir() string {
	return MustExecutableDir() + "/../assets/" + buildVariant + "/" + Arch()
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

func VmgrLog() string {
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

func UserSshDir() string {
	return ensureDir(HomeDir() + "/.ssh")
}

func ExtraSshDir() string {
	return ensureDir(AppDir() + "/ssh")
}

func CliBinDir() string {
	return MustExecutableDir() + "/bin"
}

func CliXbinDir() string {
	return MustExecutableDir() + "/xbin"
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

func UserDockerDir() string {
	env := os.Getenv("DOCKER_CONFIG")
	if env != "" {
		return ensureDir(env)
	}
	return ensureDir(HomeDir() + "/.docker")
}

func DockerCliPluginsDir() string {
	return ensureDir(UserDockerDir() + "/cli-plugins")
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
