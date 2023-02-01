package main

import (
	"os"
	"path"

	"github.com/kdrag0n/macvirt/macvmgr/conf/appid"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/cmd"
	"github.com/kdrag0n/macvirt/scon/cmd/scli/shell"
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

func runCtl(fallbackToShell bool) error {
	emptyCmd := len(os.Args) == 1
	if len(os.Args) >= 1 && (emptyCmd || !cmd.HasCommand(os.Args[1:])) && fallbackToShell {
		// alias to run - so we borrow its arg parsing logic
		os.Args = append([]string{os.Args[0], "run"}, os.Args[1:]...)
	}

	cmd.Execute()
	return nil
}
