package sshsrv

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/orbstack/macvirt/scon/agent/envutil"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/setup/userutil"
	"github.com/orbstack/macvirt/vmgr/util/pspawn"
	"github.com/orbstack/macvirt/vmgr/vnet/gonet"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/termios"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
)

const (
	// we don't use ssh for security, so hard-code for fast startup
	internalHostKeyEd25519 = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACAttgW2GBlhevzMN+oTeLxMzHceTiuROhLxCAjXiLelUgAAAKAWOo2gFjqN
oAAAAAtzc2gtZWQyNTUxOQAAACAttgW2GBlhevzMN+oTeLxMzHceTiuROhLxCAjXiLelUg
AAAEBNbKxc45CEA2j9i1tfJGtvmYlB4thyraVGe+P1yUno0i22BbYYGWF6/Mw36hN4vEzM
dx5OK5E6EvEICNeIt6VSAAAAFmRyYWdvbkBhbmRyb21lZGEubG9jYWwBAgMEBQYH
-----END OPENSSH PRIVATE KEY-----`
)

var (
	sshSigMap = map[ssh.Signal]os.Signal{
		ssh.SIGABRT: unix.SIGABRT,
		ssh.SIGALRM: unix.SIGALRM,
		ssh.SIGFPE:  unix.SIGFPE,
		ssh.SIGHUP:  unix.SIGHUP,
		ssh.SIGILL:  unix.SIGILL,
		ssh.SIGINT:  unix.SIGINT,
		ssh.SIGKILL: unix.SIGKILL,
		ssh.SIGPIPE: unix.SIGPIPE,
		ssh.SIGQUIT: unix.SIGQUIT,
		ssh.SIGSEGV: unix.SIGSEGV,
		ssh.SIGTERM: unix.SIGTERM,
		ssh.SIGUSR1: unix.SIGUSR1,
		ssh.SIGUSR2: unix.SIGUSR2,
	}

	defaultMeta = sshtypes.SshMeta{
		RawCommand: false,
		PtyStdin:   true,
		PtyStdout:  true,
		PtyStderr:  true,
	}
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

func strp(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}

func handleSshConn(s ssh.Session) error {
	ptyReq, winCh, isPty := s.Pty()

	// new env based on mac as starting point (this is a copy)
	env := envutil.ToMap(os.Environ())

	// add everything from client
	var meta sshtypes.SshMeta
	for _, kv := range s.Environ() {
		env.SetPair(kv)
	}
	if metaStr, ok := env[sshtypes.KeyMeta]; ok {
		err := json.Unmarshal([]byte(metaStr), &meta)
		if err != nil {
			return err
		}
		delete(env, sshtypes.KeyMeta)
	} else {
		meta = defaultMeta
	}

	logrus.WithFields(logrus.Fields{
		"pty":   isPty,
		"user":  s.User(),
		"cmd":   s.RawCommand(),
		"meta":  meta,
		"argv0": strp(meta.Argv0),
	}).Debug("SSH connection")

	// pwd
	var err error
	pwd := meta.Pwd
	if pwd == "" {
		pwd, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	// make sure pwd is valid, or exec will fail
	if err := unix.Access(pwd, unix.X_OK); err != nil {
		// reset to / if not
		pwd = "/"
	}

	// set basic conn-specific envs
	if isPty {
		env["TERM"] = ptyReq.Term
	}
	env["PWD"] = pwd
	env["SSH_CONNECTION"] = "::1 0 ::1 22"

	var combinedArgs []string
	argv0 := meta.Argv0
	if meta.RawCommand {
		// raw command (JSON)
		err = json.Unmarshal([]byte(s.RawCommand()), &combinedArgs)
		if err != nil {
			return err
		}
	} else {
		// get shell in case it changed
		shell, err := userutil.GetShell()
		if err != nil {
			return err
		}
		combinedArgs = []string{shell}
		if s.RawCommand() != "" {
			combinedArgs = append(combinedArgs, "-c", s.RawCommand())
		}
		// force login shell
		base := filepath.Base(shell)
		loginArgv0 := "-" + base
		if argv0 == nil {
			argv0 = &loginArgv0
		}
	}

	// for some reason, disclaiming TCC responsibility here (e.g. to /bin/zsh) doesn't work:
	// zsh ends up with no permissions, and its children (e.g. lsd) become responsible for everything they do
	// so this is likely to break many shell setups and cause bad UX
	cmd := pspawn.CommandContext(s.Context(), combinedArgs[0], combinedArgs[1:]...)
	if argv0 != nil {
		cmd.Args[0] = *argv0
	}
	cmd.Env = env.ToPairs()
	cmd.Dir = pwd

	if isPty {
		ptyF, ttyF, err := pty.Open()
		if err != nil {
			return err
		}
		defer ptyF.Close()
		defer ttyF.Close()

		// set size
		err = pty.Setsize(ptyF, &pty.Winsize{
			Rows: uint16(ptyReq.Window.Height),
			Cols: uint16(ptyReq.Window.Width),
		})
		if err != nil {
			return err
		}

		// set term modes
		tflags, err := termios.GetTermios(ptyF.Fd())
		if err != nil {
			return err
		}
		termios.ApplySSHToTermios(ptyReq.TerminalModes, tflags)
		err = termios.SetTermiosNow(ptyF.Fd(), tflags)
		if err != nil {
			return err
		}

		go func() {
			for win := range winCh {
				err := pty.Setsize(ptyF, &pty.Winsize{
					Rows: uint16(win.Height),
					Cols: uint16(win.Width),
				})
				if err != nil {
					logrus.WithError(err).Error("pty resize failed")
				}
			}
		}()

		// which ones are pipes and which ones are ptys?
		cttyFd := -1
		if meta.PtyStdin {
			cmd.Stdin = ttyF
			go io.Copy(ptyF, s)
			cttyFd = fdStdin
		} else {
			cmd.Stdin = s
		}

		if meta.PtyStdout {
			cmd.Stdout = ttyF
			cttyFd = fdStdout
		} else {
			cmd.Stdout = s
		}
		if meta.PtyStderr {
			cmd.Stderr = ttyF
			cttyFd = fdStderr
		} else {
			cmd.Stderr = s.Stderr()
		}
		if meta.PtyStdout || meta.PtyStderr {
			go io.Copy(s, ptyF)
		}

		// hook up controlling tty and session
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Setsid:  true,
			Setctty: true,
			Ctty:    cttyFd, // must always be tty
		}
	} else {
		cmd.Stdin = s
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()
	}

	err = cmd.Start()
	if err != nil {
		return err
	}

	// forward signals
	fwdSigChan := make(chan ssh.Signal, 1)
	// on stop, unregister the channel first, then close it
	// sends are protected by the session mutex, so sends to the old channel are not possible after this
	// this won't deadlock: the goroutine will keep consuming signals until the channel is closed,
	// and the channel isn't closed until after nothing can send to it anymore
	defer close(fwdSigChan)
	defer s.Signals(nil)
	s.Signals(fwdSigChan)
	go func() {
		for sshSig := range fwdSigChan {
			sig := sshSigMap[sshSig]
			if sig == nil {
				logrus.WithField("sig", sshSig).Error("unknown SSH signal")
				return
			}

			err := cmd.Process.Signal(sig)
			if err != nil {
				if errors.Is(err, os.ErrProcessDone) {
					return
				} else {
					logrus.Error("SSH signal forward failed: ", err)
				}
			}
		}
	}()

	// don't wait for fds to close, we close them
	// read-side pipes will be closed after start
	// write-side pipes will be closed on EOF
	ps, err := cmd.Process.Wait()
	if err != nil {
		return err
	}
	if !ps.Success() {
		return &exec.ExitError{ProcessState: ps}
	}

	return nil
}

func ListenHostSSH(stack *stack.Stack, address tcpip.Address) error {
	handler := func(s ssh.Session) {
		defer s.Close()

		err := handleSshConn(s)
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				// all ok, just exit
				s.Exit(exitErr.ExitCode())
			} else {
				logrus.Error("SSH error: ", err)
				s.Stderr().Write([]byte(err.Error() + "\r\n"))
				s.Exit(1)
			}
		}

		s.Exit(0)
	}

	listener, err := gonet.ListenTCP(stack, tcpip.FullAddress{
		Addr: address,
		Port: ports.SecureSvcHostSSH,
	}, ipv4.ProtocolNumber)
	if err != nil {
		return err
	}

	go func() {
		err = ssh.Serve(listener, handler, ssh.HostKeyPEM([]byte(internalHostKeyEd25519)))
		if err != nil && !errors.Is(err, io.EOF) {
			logrus.Error("hostssh: Serve() =", err)
		}
	}()

	return nil
}
