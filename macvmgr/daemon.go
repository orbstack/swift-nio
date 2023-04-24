package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/kdrag0n/macvirt/macvmgr/buildid"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/flock"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
)

func getSpawnBuildID() (string, error) {
	// reuse calculation if available as arg
	if len(os.Args) > 2 {
		return os.Args[2], nil
	}

	return buildid.CalculateCurrent()
}

func runSpawnDaemon() {
	// try process
	var buildID string
	var err error
	pid, err := flock.ReadPid(conf.VmgrLockFile())
	check(err)
	if vmclient.IsRunning() || pid != 0 {
		// check version, replace if changed
		buildID, err = getSpawnBuildID()
		check(err)

		runningBuildID, err := os.ReadFile(conf.VmgrTimestampFile())
		if err == nil && buildID == string(runningBuildID) {
			fmt.Println(pid)
			return
		}

		// replace it.
		// 1. shut down
		err = vmclient.Client().Stop()
		check(err)

		// 2. continue... below
	}

	if buildID == "" {
		buildID, err = getSpawnBuildID()
		check(err)
	}

	// exec self without spawn-daemon
	exe, err := os.Executable()
	check(err)

	logFile, err := os.Create(conf.VmManagerLog())
	check(err)

	cmd := exec.Command(exe, "vmgr", "-build-id", buildID)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	err = cmd.Start()
	check(err)

	// print pid
	fmt.Println(cmd.Process.Pid)

	os.Exit(0)
}
