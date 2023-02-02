package main

import (
	"os"
	"path"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/cmd"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/shell"
)

const (
	shortCmdHelp = `Control and interact with MacVirt Linux distros from macOS.

The listed commands can be used with either "moonctl" or "moon".

You can also prefix commands with "moon" to run them on Linux. For example:
	moon uname -a
will run "uname -a" on macOS, and is equivalent to:
	moonctl run uname -a

In this mode, the default user (matching your macOS username) and last-used distro will be used.

Usage:
	moonctl [command]

Available Commands:
  completion  Generate the autocompletion script for the specified shell
  config      Configure the Linux virtual machine
  create      Create a new Linux machine
  delete      Delete a Linux machine
  help        Help about any command
  info        Get information about a Linux machine
  list        List all Linux machines
  pull        Copy files from Linux
  push        Copy files to Linux
  reset       Delete all Linux and Docker data
  run         Run command on Linux
  shutdown    Stop the lightweight Linux virtual machine
  start       Start a Linux machine
  stop        Stop a Linux machine

Flags:
	-h, --help   help for moonctl

Use "moonctl [command] --help" for more information about a command.`
)
func main() {
	cmd := path.Base(os.Args[0])
	var err error
	exitCode := 0
	switch cmd {
	// control-only command mode
	case appid.ShortCtl, "lnxctl", "scli":
		err = runCtl(false)
	// control or shell, depending on args
	case appid.ShortCmd, "lnx":
		err = runCtl(true)
	// command stub mode
	default:
		exitCode, err = runCommandStub(cmd)
	}

	if err != nil {
		panic(err)
	}

	os.Exit(exitCode)
}

func runCommandStub(cmd string) (int, error) {
	args := []string{cmd}
	args = append(args, os.Args[1:]...)
	return shell.ConnectSSH(shell.CommandOpts{
		CombinedArgs: args,
	})
}

func shouldCallRunCtl(args []string) bool {
	// handled by ctl
	if cmd.HasCommand(args) {
		return false
	}

	// special cases: help, --help, -h
	// use run's arg parsing logic
	remArgs, parseErr := cmd.ParseRunFlags(args)
	if parseErr != nil {
		return false
	}

	// is this help command or -h/--help flag? if so, let root cmd handle it
	if cmd.FlagWantHelp || (len(remArgs) > 0 && remArgs[0] == "help") {
		return false
	}

	return true
}

func runCtl(fallbackToShell bool) error {
	if fallbackToShell && shouldCallRunCtl(os.Args[1:]) {
		// alias to run - so we borrow its arg parsing logic
		os.Args = append([]string{os.Args[0], "run"}, os.Args[1:]...)
	}

	cmd.Execute()
	return nil
}
