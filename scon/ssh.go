package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/conf/sshenv"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/termios"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

const (
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

type ExitError struct {
	status int
}

func (e *ExitError) ExitCode() int {
	return e.status
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("exit status %d", e.status)
}

type SshServer struct {
}

func (m *ConManager) handleSSHConn(s ssh.Session) (printErr bool, err error) {
	ptyReq, winCh, isPty := s.Pty()
	printErr = isPty

	// user and container
	userReq := s.User()
	userParts := strings.Split(userReq, "@")
	var user, containerName string
	if len(userParts) > 2 {
		err = fmt.Errorf("invalid user: %s", userReq)
		return
	}
	if len(userParts) == 2 {
		user = userParts[0]
		containerName = userParts[1]
	} else {
		// default user = host user
		user, err = m.defaultUser()
		if err != nil {
			return
		}
		containerName = userParts[0]
	}

	// default container?
	defaultContainerObj, err := m.GetDefaultContainer()
	if err != nil {
		return
	}
	defaultContainer := defaultContainerObj.Name
	if containerName == "default" {
		containerName = defaultContainer
	}

	// default user?
	if user == "[default]" {
		user, err = m.defaultUser()
		if err != nil {
			return
		}
	}

	container, ok := m.GetByName(containerName)
	// try default container
	if !ok && len(userParts) == 1 {
		container, ok = m.GetByName(defaultContainer)
		if ok {
			containerName = defaultContainer
			user = userParts[0]
		}
	}
	if !ok {
		err = fmt.Errorf("container not found: %s", containerName)
		return
	}

	if !conf.Debug() && container.builtin {
		err = fmt.Errorf("cannot enter builtin container: %s", containerName)
		return
	}

	// hack for debug testing
	if conf.Debug() {
		switch user {
		case "stop":
			err = container.Stop()
			return
		case "delete":
			err = container.Delete()
			return
		case "start":
			err = container.Start()
			return
		case "freeze":
			err = container.Freeze()
			return
		case "unfreeze":
			err = container.Unfreeze()
			return
		}
	}

	// set as last container
	go m.db.SetLastContainerID(container.ID)

	if !container.Running() {
		logrus.WithFields(logrus.Fields{
			"container": containerName,
		}).Info("starting container for ssh")

		err = container.Start()
		if err != nil {
			return
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
		err = json.Unmarshal([]byte(metaStr), &meta)
		if err != nil {
			return
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
	env = filterEnv(env, sshenv.NoInheritEnvs)

	// pwd
	cwd, err := container.Agent().ResolveSSHDir(agent.ResolveSSHDirArgs{
		User: user,
		Dir:  meta.Pwd,
	})
	if err != nil {
		return
	}

	// env: set TERM and PWD
	if isPty {
		env = append(env, "TERM="+ptyReq.Term)
	}
	env = append(env, "PWD="+cwd)

	var suCmd string
	prelude := "cd " + shellescape.Quote(cwd) + "; " + envToShell(env)
	if meta.RawCommand {
		// raw command (JSON)
		var rawArgs []string
		err = json.Unmarshal([]byte(s.RawCommand()), &rawArgs)
		if err != nil {
			return
		}
		suCmd = prelude + " exec " + shellescape.QuoteCommand(rawArgs)
	} else {
		var shellArgs []string
		if s.RawCommand() != "" {
			shellArgs = append(shellArgs, "-c", s.RawCommand())
		}
		suCmd = prelude + " exec $SHELL -l " + shellescape.QuoteCommand(shellArgs)
	}
	// this fixes job control. with -c, util-linux su calls setsid(), causing ctty to get lost
	// https://github.com/util-linux/util-linux/blob/master/login-utils/su-common.c#L1269
	commandArg := "--session-command"
	// Busybox su doesn't support --session-command, so use -c instead
	// TODO better way
	if container.Image.Distro == ImageAlpine || container.Image.Distro == ImageNixos || container.Image.Distro == ImageDocker {
		commandArg = "-c"
	}
	combinedArgs := []string{"su", "-l", user, commandArg, suCmd}

	cmd := &agent.AgentCommand{
		CombinedArgs: combinedArgs,
		Env:          env,
		Dir:          cwd,
		User:         user,
	}

	if isPty {
		ptyF, ttyF, err2 := container.OpenPty()
		err = err2
		if err != nil {
			return
		}
		defer ptyF.Close()
		defer ttyF.Close()

		// set size
		err = pty.Setsize(ptyF, &pty.Winsize{
			Rows: uint16(ptyReq.Window.Height),
			Cols: uint16(ptyReq.Window.Width),
		})
		if err != nil {
			return
		}

		// set term modes
		tflags, err2 := termios.GetTermios(ptyF.Fd())
		err = err2
		if err != nil {
			return
		}
		termios.ApplySSHToTermios(ptyReq.TerminalModes, tflags)
		err = termios.SetTermiosNow(ptyF.Fd(), tflags)
		if err != nil {
			return
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
		cmd.Setsid = true
		cmd.Setctty = true
		cmd.CttyFd = cttyFd
	} else {
		cmd.Stdin = s
		cmd.Stdout = s
		cmd.Stderr = s.Stderr()
	}

	err = cmd.Start(container.Agent())
	if err != nil {
		return
	}

	// now that the command has been started, don't print errors to pty
	printErr = false

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
	status, err := cmd.Process.Wait()
	if err != nil {
		logrus.Error("wait err: ", err)
		return
	}
	if status != 0 {
		err = &ExitError{status: status}
		return
	}

	err = nil
	return
}

func (m *ConManager) runSSHServer(listenIP string) error {
	handler := func(s ssh.Session) {
		defer s.Close()

		printErr, err := m.handleSSHConn(s)
		if err != nil {
			if exitErr, ok := err.(*ExitError); ok {
				// all ok, just exit
				s.Exit(exitErr.ExitCode())
			} else {
				logrus.Error("SSH error: ", err)
				if printErr {
					s.Stderr().Write([]byte(err.Error() + "\r\n"))
				}
				s.Exit(1)
			}
		}

		s.Exit(0)
	}

	listenerInternal, err := net.Listen("tcp", net.JoinHostPort(listenIP, strconv.Itoa(ports.GuestSconSSH)))
	if err != nil {
		return err
	}

	listenerPublic, err := net.Listen("tcp", net.JoinHostPort(listenIP, strconv.Itoa(ports.GuestSconSSHPublic)))
	if err != nil {
		return err
	}

	hostKeyOpt := ssh.HostKeyPEM([]byte(hostKeyEd25519))
	go runOne("internal SSH server", func() error {
		return ssh.Serve(listenerInternal, handler, hostKeyOpt)
	})

	pubKeyStr, err := m.host.GetSSHPublicKey()
	if err != nil {
		return err
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKeyStr))
	if err != nil {
		return err
	}

	pubKeyOpt := ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
		return ssh.KeysEqual(key, pubKey)
	})
	go runOne("public SSH server", func() error {
		return ssh.Serve(listenerPublic, handler, hostKeyOpt, pubKeyOpt)
	})

	return nil
}
