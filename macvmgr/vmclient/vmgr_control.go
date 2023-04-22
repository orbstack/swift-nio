package vmclient

import (
	"errors"
	"net"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/util"
	"github.com/kdrag0n/macvirt/scon/sclient"
)

const (
	startPollInterval = 100 * time.Millisecond
	// for VM and scon each
	startTimeout = 15 * time.Second
)

func IsRunning() bool {
	// try dialing
	conn, err := net.Dial("unix", conf.VmControlSocket())
	if err != nil {
		return false
	}
	defer conn.Close()

	return true
}

func IsSconRunning() (bool, error) {
	client, err := sclient.New("unix", conf.SconRPCSocket())
	if err != nil {
		return false, err
	}
	defer client.Close()

	err = client.Ping()
	if err != nil {
		return false, nil
	}

	return true, nil
}

func FindVmgrExe() (string, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", err
	}

	// resolve symlinks
	selfExe, err = filepath.EvalSymlinks(selfExe)
	if err != nil {
		return "", err
	}

	return path.Join(path.Dir(selfExe), "OrbStack Helper (VM)"), nil
}

func SpawnDaemon(newBuildID string) error {
	// start it. assume executable is next to ours, unless this is debug
	vmgrExe, err := FindVmgrExe()
	if err != nil {
		return err
	}

	// exec self with spawn-daemon
	args := []string{vmgrExe, "spawn-daemon"}
	if newBuildID != "" {
		args = append(args, newBuildID)
	}
	_, err = util.Run(args...)
	if err != nil {
		return err
	}

	return nil
}

func EnsureVM() error {
	if !IsRunning() {
		err := SpawnDaemon("")
		if err != nil {
			return err
		}

		// wait for VM to start
		before := time.Now()
		for !IsRunning() {
			if time.Since(before) > startTimeout {
				return errors.New("timed out waiting for VM to start")
			}

			time.Sleep(startPollInterval)
		}
	}

	return nil
}

func EnsureSconVM() error {
	// ensure VM first
	err := EnsureVM()
	if err != nil {
		return err
	}

	// wait for sconrpc to start
	before := time.Now()
	for {
		if time.Since(before) > startTimeout {
			return errors.New("timed out waiting for machine manager to start")
		}

		isRunning, err := IsSconRunning()
		if err != nil {
			return err
		}
		if isRunning {
			break
		}

		time.Sleep(startPollInterval)
	}

	return nil
}

func IsUpdatePending() (bool, error) {
	// check for update file
	_, err := os.Stat(conf.UpdatePendingFlag())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		} else {
			return false, err
		}
	}

	return true, nil
}
