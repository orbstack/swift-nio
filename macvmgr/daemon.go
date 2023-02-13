package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/kdrag0n/macvirt/macvmgr/buildid"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
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
	// try dialing vmcontrol
	var buildID string
	var err error
	if vmclient.IsRunning() {
		// check version, replace if changed
		buildID, err = getSpawnBuildID()
		check(err)

		runningBuildID, err := os.ReadFile(conf.VmgrVersionFile())
		if err == nil && buildID == string(runningBuildID) {
			fmt.Println("already running")
			return
		}

		// replace it.
		// 1. shut down
		fmt.Println("stopping old daemon")
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

	fmt.Println("spawning daemon")
	logFile, err := os.Create(conf.VmManagerLog())
	check(err)

	cmd := exec.Command(exe, "vmgr", buildID)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	err = cmd.Start()
	check(err)

	os.Exit(0)
}
