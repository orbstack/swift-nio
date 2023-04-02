package main

import (
	"fmt"
	"os"
	"path"

	"github.com/fatih/color"
	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/cmd"
	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/shell"
)

func main() {
	cmd := path.Base(os.Args[0])
	var err error
	exitCode := 0
	switch cmd {
	// control-only command mode
	case "macctl":
		err = runCtl(false)
	// control or shell, depending on args
	case "mac":
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

	// what command are we running?
	baseCmd := path.Base(args[0])
	if baseCmd == "open" {
		// "open" is a special case where we know args that look like paths must be paths, so we can safely translate them w/o relaxed
		args = shell.TranslateArgPaths(args)
	} else {
		// we can't safely do this kind of translation for everything. e.g. adb
		args = shell.TranslateArgPathsRelaxed(args)
	}
	return shell.ConnectSSH(shell.CommandOpts{
		CombinedArgs: args,
	})
}

func printShortHelp() {
	bold := color.New(color.Bold, color.FgHiBlue).SprintFunc()
	fmt.Printf(`OrbStack's short "mac" command can be used in 3 ways:

%s
   Just run "mac" with no arguments.
   Usage: mac

%s
   Prefix any command with "mac" to run it on Mac.
   Usage: mac [flags] <command> [args...]
   Example: mac uname -a

   Use "macctl run --help" for a list of flags.

%s
   Send macOS notifications, copy files, and more with any "macctl" subcommand.
   Usage: mac <subcommand> [args...]

   Use "macctl --help" for a list of subcommands.
`, bold("1. Start a Mac shell."), bold(`2. Run commands on Mac, like "macctl run".`), bold(`3. Interact with Mac, like "macctl".`))
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
