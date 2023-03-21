package scli

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/fatih/color"
	"github.com/kdrag0n/macvirt/macvmgr/buildid"
	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/drm/killswitch"
	"github.com/kdrag0n/macvirt/macvmgr/vmclient"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/spinutil"
	"golang.org/x/term"
)

const (
	refMsg = `
    ╭───────────────────────────────────────────────────────╮
    │                                                       │
    │              OrbStack update available!               │
    │              Run "orb update" to update.              │
    │                                                       │
	│  Updates include improvements, features, and fixes.   │
	│            This version expires in %2d days.           │
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
	oldVersion, err := os.ReadFile(conf.VmgrTimestampFile())
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
    │`)
		fmt.Fprint(os.Stderr, `            This version expires in `)
		purple.Fprintf(os.Stderr, `%2d`, int(math.Round(time.Until(killswitch.ExpiryTime).Hours()/24)))
		fmt.Fprint(os.Stderr, ` days.           `)
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
