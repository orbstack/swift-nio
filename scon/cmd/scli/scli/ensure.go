package scli

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/fatih/color"
	"github.com/orbstack/macvirt/scon/cmd/scli/spinutil"
	"github.com/orbstack/macvirt/vmgr/buildid"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/vmclient"
	"golang.org/x/term"
)

const (
	// for easy editing/reference purposes
	refMsg = `
    ╭───────────────────────────────────────────────────────╮
    │                                                       │
    │              OrbStack update available!               │
    │              Run "orb update" to update.              │
    │                                                       │
	│  Updates include improvements, features, and fixes.   │
    │                                                       │
    ╰───────────────────────────────────────────────────────╯
`
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

func tryPrintUpdateWarning() {
	if conf.Debug() {
		return
	}

	needsUpdate, err := vmclient.IsUpdatePending()
	if err != nil {
		return
	}

	if needsUpdate && term.IsTerminal(int(os.Stderr.Fd())) {
		yellow := color.New(color.FgYellow)
		purple := color.New(color.FgMagenta)
		bold := color.New(color.Bold, color.FgHiBlue)
		yellow.Fprint(os.Stderr, `
    ╭───────────────────────────────────────────────────────╮
    │                                                       │
    │`)
		bold.Fprint(os.Stderr, `              OrbStack update available!               `)
		yellow.Fprint(os.Stderr, `│
    │`)
		fmt.Fprint(os.Stderr, `              Run "`)
		purple.Fprint(os.Stderr, `orb update`)
		fmt.Fprint(os.Stderr, `" to update.              `)
		yellow.Fprint(os.Stderr, `│
    │                                                       │
    │`)
		fmt.Fprint(os.Stderr, `  Updates include improvements, features, and fixes.   `)
		yellow.Fprint(os.Stderr, `│
    │                                                       │
    ╰───────────────────────────────────────────────────────╯

`)
	}
}

func updateVmgr() bool {
	newBuildID, shouldUpdate := shouldUpdateVmgr()
	if !shouldUpdate {
		tryPrintUpdateWarning()
		return false
	}

	spinner := spinutil.Start("blue", "Updating service")
	_, err := vmclient.SpawnDaemon(newBuildID)
	spinner.Stop()
	checkCLI(err)

	return true
}

var ensureOnce = sync.OnceValue(func() bool {
	if vmclient.IsRunning() && os.Getenv("ORB_TEST") != "1" {
		if os.Getenv("ORB_DEV") == "1" {
			if _, s := shouldUpdateVmgr(); s {
				color.New(color.FgYellow).Fprintln(os.Stderr, "Note: usually we'd restart vmgr, but since you're special, we're going to ignore it. Here be dragons!\n")
			}
		} else {
			if !updateVmgr() {
				return true
			}
		}
	}

	return false
})

func EnsureVMWithSpinner() {
	// true = early return
	shouldReturn := ensureOnce()
	if shouldReturn {
		return
	}

	spinner := spinutil.Start("green", "Starting machine")
	err := vmclient.EnsureVM()
	spinner.Stop()
	checkCLI(err)
}

func EnsureSconVMWithSpinner() {
	// good enough. delay is short and this is much faster
	shouldReturn := ensureOnce()
	if shouldReturn {
		return
	}

	spinner := spinutil.Start("green", "Starting machine")
	err := vmclient.EnsureSconVM()
	spinner.Stop()
	checkCLI(err)
}
