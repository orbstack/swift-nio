package coredir

import (
	"errors"
	"os"
	"sync"

	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

var (
	ensuredDirsMu sync.Mutex
	ensuredDirs   = make(map[string]string)

	overrideHomeDir string
)

func SetOverrideHomeDir(dir string) {
	overrideHomeDir = dir
}

func HomeDir() string {
	if overrideHomeDir != "" {
		return overrideHomeDir
	}

	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return home
}

func AppDir() string {
	return ensureDir(HomeDir() + "/." + appid.AppName)
}

func ensureDir(dir string) string {
	_, err := EnsureDir(dir)
	if err != nil {
		panic(err)
	}
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

// TODO: migrate to /config
func VmConfigFile() string {
	env := os.Getenv("ORB_VMGR_CONFIG")
	if env != "" {
		return env
	}

	return AppDir() + "/vmconfig.json"
}

func VmStateFile() string {
	return AppDir() + "/vmstate.json"
}

// used in linux macctl
func NfsMountpoint() string {
	return ensureDir(HomeDir() + "/" + mounts.NfsDirName)
}
