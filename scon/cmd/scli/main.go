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
	case "lnxctl":
		fallthrough
	case "scli": // for Linux testing
		fallthrough
	case appid.ShortCmd + "ctl":
		err = runCtl(false)
	// control or shell, depending on args
	case "lnx":
		fallthrough
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
	return shell.ConnectSSH(shell.CommandOpts{
		CombinedArgs: args,
	})
}

func runCtl(fallbackToShell bool) error {
	emptyCmd := len(os.Args) == 1
	if len(os.Args) >= 1 && (emptyCmd || !cmd.HasCommand(os.Args[1:])) && fallbackToShell {
		exitCode, err := shell.ConnectSSH(shell.CommandOpts{
			CombinedArgs: os.Args[1:],
		})
		if err != nil {
			return err
		}

		os.Exit(exitCode)
	}

	cmd.Execute()
	return nil
}
