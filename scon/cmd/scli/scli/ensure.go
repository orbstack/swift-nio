package scli

import (
	"fmt"
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
)

func checkCLI(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func EnsureVMWithSpinner() {
	if vmclient.IsRunning() {
		return
	}

	spinner := spinutil.Start("green", "Starting virtual machine")
	err := vmclient.EnsureVM()
	spinner.Stop()
	checkCLI(err)
}

func EnsureSconVMWithSpinner() {
	spinner := spinutil.Start("green", "Starting virtual machine")
	err := vmclient.EnsureSconVM()
	spinner.Stop()
	checkCLI(err)
}
