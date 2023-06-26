package vmclient

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/orbstack/macvirt/scon/sclient"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/util"
	"golang.org/x/sys/unix"
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

func isProcessRunning(pid int) bool {
	// try sending signal 0 to the process
	err := unix.Kill(pid, 0)
	return err == nil
}

func FindVmgrExe() (string, error) {
	exeDir, err := conf.ExecutableDir()
	if err != nil {
		return "", err
	}
	return path.Join(exeDir, conf.VmgrExeName), nil
}

func SpawnDaemon(newBuildID string) (int, error) {
	// start it. assume executable is next to ours, unless this is debug
	vmgrExe, err := FindVmgrExe()
	if err != nil {
		return 0, fmt.Errorf("find vmgr exe: %w", err)
	}

	// exec self with spawn-daemon
	args := []string{vmgrExe, "spawn-daemon"}
	if newBuildID != "" {
		args = append(args, newBuildID)
	}
	out, err := util.Run(args...)
	if err != nil {
		return 0, err
	}

	pid, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse pid: %w", err)
	}

	return pid, nil
}

func readVmgrLogs() (string, error) {
	logs, err := os.ReadFile(conf.VmgrLog())
	if err != nil {
		return "", fmt.Errorf("read vmgr logs: %w", err)
	}

	return string(logs), nil
}

func EnsureVM() error {
	if !IsRunning() {
		pid, err := SpawnDaemon("")
		if err != nil {
			return fmt.Errorf("spawn daemon: %w", err)
		}

		// wait for VM to start
		before := time.Now()
		for !IsRunning() {
			if !isProcessRunning(pid) {
				// process exited. let's read logs
				logs, err := readVmgrLogs()
				if err != nil {
					return fmt.Errorf("VM exited unexpectedly; failed to read logs: %w", err)
				}

				return fmt.Errorf("VM exited unexpectedly; logs:\n%s", logs)
			}
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
		return fmt.Errorf("start VM: %w", err)
	}

	// wait for sconrpc to start
	before := time.Now()
	for {
		if time.Since(before) > startTimeout {
			return errors.New("timed out waiting for services to start")
		}

		isRunning, err := IsSconRunning()
		if err != nil {
			return fmt.Errorf("check scon running: %w", err)
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
