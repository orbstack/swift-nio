package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
)

func runSpawnDaemon() {
	// exec self without spawn-daemon
	exe, err := os.Executable()
	check(err)

	// try dialing vmcontrol
	if vmclient.IsRunning() {
		return
	}

	fmt.Println("spawning daemon")
	logFile, err := os.Create(conf.VmManagerLog())
	check(err)

	cmd := exec.Command(exe)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	err = cmd.Start()
	check(err)

	os.Exit(0)
}
