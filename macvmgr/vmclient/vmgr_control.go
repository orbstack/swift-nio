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

func EnsureVM() error {
	if !IsRunning() {
		// start it. assume executable is next to ours, unless this is debug
		var vmgrExe string
		if conf.Debug() {
			vmgrExe = "/Users/dragon/code/projects/macvirt/macvmgr/macvmgr"
		} else {
			selfExe, err := os.Executable()
			if err != nil {
				return err
			}

			vmgrExe = path.Join(path.Dir(selfExe), "macvmgr")
		}

		// exec self with spawn-daemon
		cmd := exec.Command(vmgrExe, "spawn-daemon")
		err := cmd.Start()
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
