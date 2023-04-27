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

func tryStopOld() error {
	client, err := vmclient.NewClient()
	if err != nil {
		return err
	}

	// try to stop
	err = client.SyntheticStopOrKill()
	if err != nil {
		// didn't work. vmclient already checked flock and killed it if there was a pid, so nothing else we can do...
		return err
	}

	return nil
}

func maybeStopOld(canRecurse bool) (string, error) {
	// try process
	var buildID string
	var err error
	pid, err := flock.ReadPid(conf.VmgrLockFile())
	if err != nil {
		return "", err
	}
	if pid != 0 {
		// check version, replace if changed
		buildID, err = getSpawnBuildID()
		if err != nil {
			return "", err
		}

		runningBuildID, err := os.ReadFile(conf.VmgrTimestampFile())
		if err == nil && buildID == string(runningBuildID) {
			// we found an existing one and it's the same version
			// return the pid and use it
			fmt.Println(pid)
			os.Exit(0)
		}

		// replace it.
		// 1. try to shut down
		// we CAN'T use vmclient.Client because it could recurse into spawn-daemon
		err = tryStopOld()
		if err != nil {
			// if it didn't work, check if it's still running and what version it is now
			// we could've raced with another spawn-daemon upgrade - so max 1 try
			if canRecurse {
				return maybeStopOld(false)
			} else {
				return "", err
			}
		}

		// 2. continue... below
	} else if vmclient.IsRunning() {
		// if socket is running but flock PID is not, we must stop it and restart
		// GUI will not work properly with socket but no lock
		err = tryStopOld()
		if err != nil {
			return "", err
		}
	}

	return buildID, nil
}

func runSpawnDaemon() {
	buildID, err := maybeStopOld(true)
	check(err)

	if buildID == "" {
		buildID, err = getSpawnBuildID()
		check(err)
	}

	// exec self without spawn-daemon
	exe, err := os.Executable()
	check(err)

	// rotate the log if needed
	if _, err := os.Lstat(conf.VmgrLog()); err == nil {
		// exists, so rename it
		err = os.Rename(conf.VmgrLog(), conf.VmgrLog()+".1")
		check(err)
	}
	logFile, err := os.Create(conf.VmgrLog())
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
