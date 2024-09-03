package conf

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
)

const (
	VmgrExeName = "OrbStack Helper"

	K8sContext = appid.AppName
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

func DiagDir() string {
	return ensureDir(AppDir() + "/diag")
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
		rootContentsPath := strings.TrimSuffix(selfExe, nestedBundleSuffix)
		return rootContentsPath + "/MacOS", nil
	}

	selfDir := filepath.Dir(selfExe)
	if Debug() {
		// in debug, we are $MACVIRT/out/OrbStack Helper.app/Contents/MacOS (vmgr) or $MACVIRT/out/scli (scli)
		// find the /out/ part to get repo root
		outIndex := strings.Index(selfDir, "/out")
		if outIndex == -1 {
			return "", fmt.Errorf("unexpected debug path: %s", selfDir)
		}
		selfDir = selfDir[:outIndex]
		selfDir += "/swift/DerivedData/MacVirt/Build/Products/Debug/OrbStack.app/Contents/MacOS"
	}

	return selfDir, nil
}

func MustExecutableDir() string {
	dir, err := ExecutableDir()
	if err != nil {
		panic(err)
	}
	return dir
}

func ResourcesDir() string {
	// simpler for development
	return MustExecutableDir() + "/../Resources"
}

func AssetsDir() string {
	return ResourcesDir() + "/assets/" + buildVariant + "/" + Arch()
}

func GetAssetFile(name string) string {
	return AssetsDir() + "/" + name
}

func DockerSocket() string {
	return RunDir() + "/docker.sock"
}

func DockerRemoteCtxSocket() string {
	return coredir.HomeDir() + "/.docker/run/docker.sock"
}

func KubeDir() string {
	return ensureDir(HomeDir() + "/.kube")
}

func KubeConfigFile() string {
	return KubeDir() + "/config"
}

func OrbK8sDir() string {
	return ensureDir(AppDir() + "/k8s")
}

func OrbK8sConfigFile() string {
	return OrbK8sDir() + "/config.yml"
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

func VmgrLog1() string {
	// should be .log.1 but this is easier to view on macOS and iOS
	return LogDir() + "/vmgr.1.log"
}

func VmgrSampleLog() string {
	return LogDir() + "/vmgr.sample.log"
}

func VmgrVersionFile() string {
	return RunDir() + "/vmgr.version"
}

// for QueueDirectories trigger
func StatusDir() string {
	return ensureDir(RunDir() + "/status")
}

func StatusFileRunning() string {
	return StatusDir() + "/running"
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

func CliZshCompletionsDir() string {
	return ResourcesDir() + "/completions/zsh"
}

func CliCompletionsDir() string {
	return ResourcesDir() + "/completions"
}

func FishCompletionsDir() string {
	return HomeDir() + "/.config/fish/completions"
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
