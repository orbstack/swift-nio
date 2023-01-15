package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path"
	"strconv"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/termios"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/terminal"
	"golang.org/x/sys/unix"
)

const (
	// TODO share w/ vnet. this avoids importing all of vnet
	ServicesIP4 = "172.30.30.200"
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

func translatePath(p string) string {
	// canonicalize first
	p = path.Clean(p)

	// if path is under mac virtiofs mount, remove the mount prefix
	if p == mounts.VirtiofsMountpoint {
		return "/"
	} else if strings.HasPrefix(p, mounts.VirtiofsMountpoint+"/") {
		return strings.TrimPrefix(p, mounts.VirtiofsMountpoint)
	}

	// nothing to do for linked paths
	for _, linkPrefix := range mounts.LinkedPaths {
		if p == linkPrefix || strings.HasPrefix(p, linkPrefix+"/") {
			return p
		}
	}

	// otherwise, translate to linux
	// TODO *for this container*
	// TODO probably have to move to mac side
	nfsMountpoint := "/Users/dragon/Linux/Root"
	return nfsMountpoint + p
}

func runCommandStub(cmd string) (int, error) {
	args := []string{cmd}
	args = append(args, os.Args[1:]...)
	return connectSSH(args)
}

func connectSSH(combinedArgs []string) (int, error) {
	config := &ssh.ClientConfig{
		User: "macctl", // unused, only one user
		// Auth: []ssh.AuthMethod{
		// 	ssh.Password("test"),
		// },
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", ServicesIP4+":"+strconv.Itoa(ports.ServiceHostSSH), config)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return 0, err
	}
	defer session.Close()

	meta := sshtypes.SshMeta{
		RawCommand: true,
	}

	ptyFd := 1 // based on stdout
	if terminal.IsTerminal(ptyFd) {
		term := os.Getenv("TERM")
		w, h, err := terminal.GetSize(ptyFd)
		if err != nil {
			return 0, err
		}

		// snapshot the flags
		flags, err := termios.GetTermios(uintptr(ptyFd))
		if err != nil {
			return 0, err
		}
		modes := termios.TermiosToSSH(flags)

		// raw mode
		state, err := terminal.MakeRaw(ptyFd)
		if err != nil {
			return 0, err
		}
		defer terminal.Restore(ptyFd, state)

		// request pty
		err = session.RequestPty(term, h, w, modes)
		if err != nil {
			return 0, err
		}
	}

	session.Stdin = os.Stdin
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// forward and translate cwd path
	cwd, err := os.Getwd()
	if err == nil {
		meta.Pwd = translatePath(cwd)
	}

	// forward signals
	fwdSigChan := make(chan os.Signal, 1)
	notifySigs := make([]os.Signal, 0)
	for k := range sshSigMap {
		notifySigs = append(notifySigs, k)
	}
	signal.Notify(fwdSigChan, notifySigs...)

	// handle window change
	winchChan := make(chan os.Signal, 1)
	signal.Notify(winchChan, unix.SIGWINCH)

	// send environment (server chooses what to accept)
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		session.Setenv(key, value)
	}

	// send metadata
	metaBytes, err := json.Marshal(&meta)
	if err != nil {
		return 0, err
	}
	session.Setenv("__MV_META", string(metaBytes))

	if len(combinedArgs) > 0 {
		// run $0
		// TODO find and translate paths
		combinedArgsBytes, err := json.Marshal(&combinedArgs)
		if err != nil {
			return 0, err
		}
		err = session.Start(string(combinedArgsBytes))
		if err != nil {
			return 0, err
		}
	} else {
		// no args = shell
		err = session.Shell()
		if err != nil {
			return 0, err
		}
	}

	// wait for done
	doneChan := make(chan error, 1)
	go func() {
		doneChan <- session.Wait()
	}()

	// handle signals, WINCH, and done
	for {
		select {
		case sig := <-fwdSigChan:
			err = session.Signal(sshSigMap[sig])
		case <-winchChan:
			w, h, err := terminal.GetSize(ptyFd)
			if err != nil {
				continue
			}

			err = session.WindowChange(h, w)
		case err := <-doneChan:
			if err != nil {
				if exitErr, ok := err.(*ssh.ExitError); ok {
					return exitErr.ExitStatus(), nil
				} else {
					return 0, err
				}
			}

			return 0, nil
		}
	}
}

func runCtl(fallbackToShell bool) error {
	return nil
}
