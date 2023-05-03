package conf

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
)

var (
	ensuredDirsMu sync.Mutex
	ensuredDirs   = make(map[string]string)
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
	_, err := EnsureDir(dir)
	check(err)
	return dir
}

func EnsureDir(dir string) (string, error) {
	ensuredDirsMu.Lock()
	defer ensuredDirsMu.Unlock()

	if d, ok := ensuredDirs[dir]; ok {
		return d, nil
	}
	defer func() {
		ensuredDirs[dir] = dir
	}()

	// stat first
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		return dir, nil
	}

	err := os.MkdirAll(dir, 0755)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return dir, nil
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

func ExecutableDir() (string, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get exe path: %w", err)
	}

	// resolve symlinks
	selfExe, err = filepath.EvalSymlinks(selfExe)
	if err != nil {
		return "", fmt.Errorf("resolve symlinks: %w", err)
	}
	return filepath.Dir(selfExe), nil
}

func MustExecutableDir() string {
	dir, err := ExecutableDir()
	check(err)
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
