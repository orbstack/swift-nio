package main

import (
	"os"
	"path"

	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/cmd"
	"github.com/kdrag0n/macvirt/macvmgr/cmd/macctl/shell"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

var (
	sshSigMap = map[os.Signal]ssh.Signal{
		unix.SIGABRT: ssh.SIGABRT,
		unix.SIGALRM: ssh.SIGALRM,
		unix.SIGFPE:  ssh.SIGFPE,
		unix.SIGHUP:  ssh.SIGHUP,
		unix.SIGILL:  ssh.SIGILL,
		unix.SIGINT:  ssh.SIGINT,
		unix.SIGPIPE: ssh.SIGPIPE,
		unix.SIGQUIT: ssh.SIGQUIT,
		unix.SIGSEGV: ssh.SIGSEGV,
		unix.SIGTERM: ssh.SIGTERM,
		unix.SIGUSR1: ssh.SIGUSR1,
		unix.SIGUSR2: ssh.SIGUSR2,
	}
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
	return shell.ConnectSSH(shell.CommandOpts{
		CombinedArgs: args,
	})
}

func runCtl(fallbackToShell bool) error {
	cmd.Execute()
	return nil
}
