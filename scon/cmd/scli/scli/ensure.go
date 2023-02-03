package scli

import (
	"errors"
	"fmt"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/buildid"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
)

func checkCLI(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func shouldUpdateVmgr() (string, bool) {
	oldVersion, err := os.ReadFile(conf.VmgrVersionFile())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", true
		} else {
			checkCLI(err)
		}
	}

	vmgrExe, err := vmclient.FindVmgrExe()
	checkCLI(err)

	newVersion, err := buildid.CalculatePath(vmgrExe)
	checkCLI(err)

	return newVersion, string(oldVersion) != newVersion
}

func updateVmgr() bool {
	newBuildID, shouldUpdate := shouldUpdateVmgr()
	if !shouldUpdate {
		return false
	}

	spinner := spinutil.Start("blue", "Updating machine")
	err := vmclient.SpawnDaemon(newBuildID)
	spinner.Stop()
	checkCLI(err)

	return true
}

func EnsureVMWithSpinner() {
	if vmclient.IsRunning() {
		if !updateVmgr() {
			return
		}
	}

	spinner := spinutil.Start("green", "Starting machine")
	err := vmclient.EnsureVM()
	spinner.Stop()
	checkCLI(err)
}

func EnsureSconVMWithSpinner() {
	// good enough. delay is short and this is much faster
	if vmclient.IsRunning() {
		if !updateVmgr() {
			return
		}
	}

	spinner := spinutil.Start("green", "Starting machine")
	err := vmclient.EnsureSconVM()
	spinner.Stop()
	checkCLI(err)
}
