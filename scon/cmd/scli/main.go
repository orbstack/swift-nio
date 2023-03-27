package main

import (
	"fmt"
	"os"
	"path"

	"github.com/fatih/color"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/macvmgr/drm/killswitch"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/cmd"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/shell"
)

func main() {
	// scli uses vmgr's killswitch because it's built on mac
	err := killswitch.MonitorAndExit()
	if err != nil {
		panic(err)
	}

	cmd := path.Base(os.Args[0])
	exitCode := 0
	switch cmd {
	// control-only command mode
	case appid.ShortCtl, "scli":
		err = runCtl(false)
	// control or shell, depending on args
	case appid.ShortCmd:
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
	return shell.RunSSH(shell.CommandOpts{
		CombinedArgs: args,
	})
}

func printShortHelp() {
	bold := color.New(color.Bold, color.FgHiBlue).SprintFunc()
	fmt.Printf(`OrbStack's short "orb" command has 3 usages:

%s
   Just run "orb" with no arguments.
   Usage: orb

%s
   Prefix any command with "orb" to run it on Linux.
   Usage: orb [flags] <command> [args...]
   Example: orb uname -a

   The default user and machine will be used, unless specified with flags.
   For example, to log in to "ubuntu" as root: orb -m ubuntu -u root uname -a

   Use "orbctl run --help" for a list of flags.
   If you prefer SSH, use "orbctl ssh" for details.

%s
   For convenience, you can use orbctl subcommands with this command.
   Usage: orb <subcommand> [args...]

   Use "orbctl --help" for a list of subcommands.

For Docker containers, use the "docker" command directly. "orb" is for full Linux machines.
`, bold("1. Start a Linux shell."), bold(`2. Run commands on Linux, like "orbctl run".`), bold(`3. Control Linux machines, like "orbctl".`))
	os.Exit(0)
}

func shouldCallRunCtl(args []string) bool {
	// empty = shell
	if len(args) == 0 {
		return true
	}

	// handled by ctl
	if cmd.HasCommand(args) {
		return false
	}

	// special cases: help, --help, -h
	// use run's arg parsing logic
	remArgs, parseErr := cmd.ParseRunFlags(args)
	if parseErr != nil {
		// let run handle the help
		return true
	}

	// is this help command or -h/--help flag? if so, let root cmd handle it
	if cmd.FlagWantHelp || (len(remArgs) > 0 && remArgs[0] == "help") {
		// print our help instead
		printShortHelp()
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
