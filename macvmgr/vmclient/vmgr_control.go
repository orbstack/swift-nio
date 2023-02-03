package vmclient

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
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

func FindVmgrExe() (string, error) {
	if conf.Debug() {
		return conf.HomeDir() + "/code/projects/macvirt/macvmgr/macvmgr", nil
	} else {
		selfExe, err := os.Executable()
		if err != nil {
			return "", err
		}

		return path.Join(path.Dir(selfExe), "macvmgr"), nil
	}
}

func SpawnDaemon(newBuildID string) error {
	// start it. assume executable is next to ours, unless this is debug
	vmgrExe, err := FindVmgrExe()
	if err != nil {
		return err
	}

	// exec self with spawn-daemon
	args := []string{"spawn-daemon"}
	if newBuildID != "" {
		args = append(args, newBuildID)
	}
	cmd := exec.Command(vmgrExe, args...)
	err = cmd.Run()
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

		client, err := sclient.New("unix", conf.SconRPCSocket())
		if err != nil {
			return err
		}

		err = client.Ping()
		client.Close()
		if err == nil {
			break
		}

		time.Sleep(startPollInterval)
	}

	return nil
}
