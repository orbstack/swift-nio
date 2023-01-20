package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/termios"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// TODO last used
	defaultContainer = "alpine"
	defaultUser      = "root"

	// we don't use ssh for security, so hard-code for fast startup
	hostKeyEd25519 = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAMwAAAAtzc2gtZW
QyNTUxOQAAACAgEJD3oK7ddXQktDsupy91mk85nbFM12Y6srQ0ujq4oAAAAKDLA5G2ywOR
tgAAAAtzc2gtZWQyNTUxOQAAACAgEJD3oK7ddXQktDsupy91mk85nbFM12Y6srQ0ujq4oA
AAAEAdZQRbxMDW6DaGP2YY8yxby24cwECktHygG1dGxHmuFiAQkPegrt11dCS0Oy6nL3Wa
TzmdsUzXZjqytDS6OrigAAAAFmRyYWdvbkBhbmRyb21lZGEubG9jYWwBAgMEBQYH
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

	// matches macOS ssh vars
	dropEnvs = []string{
		"USER",
		"LOGNAME",
		"HOME",
		"PATH",
		"SHELL",
		"TMPDIR",
	}
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

func envToShell(env []string) string {
	shenv := make([]string, 0, len(env))
	for _, kv := range env {
		key, val, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		shenv = append(shenv, key+"="+shellescape.Quote(val))
	}

	return strings.Join(shenv, " ")
}

func filterEnv(env []string, filter []string) []string {
	filtered := make([]string, 0, len(env))
outer:
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		for _, cand := range filter {
			if key == cand {
				continue outer
			}
		}

		filtered = append(filtered, kv)
	}

	return filtered
}

func (m *ConManager) handleSSHConn(s ssh.Session) error {
	ptyReq, winCh, isPty := s.Pty()

	// user and container
	userReq := s.User()
	userParts := strings.Split(userReq, "@")
	var user, containerName string
	if len(userParts) > 2 {
		return fmt.Errorf("invalid user: %s", userReq)
	}
	if len(userParts) == 2 {
		user = userParts[0]
		containerName = userParts[1]
	} else {
		user = defaultUser
		containerName = userParts[0]
	}
	if containerName == "default" {
		containerName = defaultContainer
	}

	container, ok := m.Get(containerName)
	// try default container
	if !ok && len(userParts) == 1 {
		container, ok = m.Get(defaultContainer)
		if ok {
			containerName = defaultContainer
			user = userParts[0]
		}
	}
	if !ok {
		return fmt.Errorf("container not found: %s", containerName)
	}

	if !container.Running() {
		fmt.Println("starting container")
		err := container.Start()
		if err != nil {
			return err
		}
	}

	// new env
	env := make([]string, 0)

	// ssh env: extract __MV_META metadata, inherit the rest
	var metaStr string
	var meta sshtypes.SshMeta
	for _, kv := range s.Environ() {
		if strings.HasPrefix(kv, "__MV_META=") {
			metaStr = kv[10:]
		} else {
			// TODO translate paths
			env = append(env, kv)
		}
	}
	if metaStr != "" {
		err := json.Unmarshal([]byte(metaStr), &meta)
		if err != nil {
			return err
		}
	} else {
		meta = defaultMeta
	}

	logrus.WithFields(logrus.Fields{
		"pty":  isPty,
		"user": s.User(),
		"cmd":  s.RawCommand(),
		"meta": meta,
	}).Debug("SSH connection")

	// remove envs inherited from container
	env = filterEnv(env, dropEnvs)

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

	// env: set TERM and PWD
	if isPty {
		env = append(env, "TERM="+ptyReq.Term)
	}
	// TODO need to translate pwd path
	env = append(env, "PWD="+pwd)

	var suCmd string
	if meta.RawCommand {
		// raw command (JSON)
		var rawArgs []string
		err = json.Unmarshal([]byte(s.RawCommand()), &rawArgs)
		if err != nil {
			return err
		}
		suCmd = envToShell(env) + " exec " + shellescape.QuoteCommand(rawArgs)
	} else {
		var shellArgs []string
		if s.RawCommand() != "" {
			shellArgs = append(shellArgs, "-c", s.RawCommand())
		}
		suCmd = envToShell(env) + " exec $SHELL -l " + shellescape.QuoteCommand(shellArgs)
	}
	combinedArgs := []string{"/bin/su", "-l", user, "-c", suCmd}

	cmd := &LxcCommand{
		CombinedArgs: combinedArgs,
		Env:          env,
		Dir:          pwd,
	}
	fmt.Println("execd:", combinedArgs)

	if isPty {
		ptyF, ttyF, err := container.OpenPty()
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
				pty.Setsize(ptyF, &pty.Winsize{
					Rows: uint16(win.Height),
					Cols: uint16(win.Width),
				})
			}
		}()

		// which ones are pipes and which ones are ptys?
		if meta.PtyStdin {
			cmd.Stdin = ttyF
			go io.Copy(ptyF, s)
		} else {
			cmd.Stdin = s
		}

		if meta.PtyStdout {
			cmd.Stdout = ttyF
		} else {
			cmd.Stdout = s
		}
		if meta.PtyStderr {
			cmd.Stderr = ttyF
		} else {
			cmd.Stderr = s.Stderr()
		}
		if meta.PtyStdout || meta.PtyStderr {
			go io.Copy(s, ptyF)
		}
	} else {
		cmd.Stdin = s
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()
	}

	err = cmd.Start(container)
	if err != nil {
		return err
	}

	// forward signals
	fwdSigChan := make(chan ssh.Signal, 1)
	defer close(fwdSigChan)
	s.Signals(fwdSigChan)
	go func() {
		for {
			sig, ok := <-fwdSigChan
			if !ok {
				return
			}

			err := cmd.Process.Signal(sshSigMap[sig])
			if err != nil {
				logrus.Error("SSH signal forward failed: ", err)
			}
		}
	}()

	// don't wait for fds to close, we close them
	// read-side pipes will be closed after start
	// write-side pipes will be closed on EOF
	fmt.Println("wait")
	ps, err := cmd.Process.Wait()
	if err != nil {
		fmt.Println("wait err:", err)
		return err
	}
	if !ps.Success() {
		fmt.Println("wait errc:", ps.ExitCode())
		return &exec.ExitError{ProcessState: ps}
	}
	fmt.Println("wait ok")

	return nil
}

func (m *ConManager) ListenSSH(address string) error {
	handler := func(s ssh.Session) {
		defer s.Close()

		err := m.handleSSHConn(s)
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

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}

	// passwordHandler := func(ctx ssh.Context, password string) bool {
	// 	return password == "test"
	// }
	go func() {
		err = ssh.Serve(listener, handler, ssh.HostKeyPEM([]byte(hostKeyEd25519))) //, ssh.PasswordAuth(passwordHandler))
		if err != nil {
			logrus.Error("hostssh: Serve() =", err)
		}
	}()

	return nil
}
