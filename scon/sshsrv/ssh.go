package sshsrv

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/termios"
	"github.com/lxc/go-lxc"
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
	inheritHostEnvs = [...]string{
		"USER",
		"LOGNAME",
		"HOME",
		"PATH",
		"SHELL",
		"TMPDIR",
		"SSH_AUTH_SOCK",
	}
	inheritHostEnvValues = []string{}
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

func init() {
	for _, kv := range os.Environ() {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		for _, cand := range inheritHostEnvs {
			if key == cand {
				inheritHostEnvValues = append(inheritHostEnvValues, kv)
				break
			}
		}
	}
}

type SpawnParams struct {
	Cwd       string
	Env       []string
	LoginUser string
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

func spawnCommandLxc(container *lxc.Container, params SpawnParams) (*exec.Cmd, error) {
	var childPid int
	attachOptions := lxc.AttachOptions{
		Namespaces: -1,
		Arch:       -1,
		Cwd:        params.Cwd,
		UID:        0,
		GID:        0,
		Groups:     nil,
		ClearEnv:   true,
		Env:        params.Env,
		EnvToKeep:  nil,
		// filled in below
		StdinFd:            0,
		StdoutFd:           0,
		StderrFd:           0,
		RemountSysProc:     false,
		ElevatedPrivileges: false,
	}

}

func handleSshConn(s ssh.Session) error {
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

	container, ok := containers[containerName]
	// try default container
	if !ok && len(userParts) == 1 {
		container, ok = containers[defaultContainer]
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

	// add all from mac
	env = append(env, os.Environ()...)

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

	// override with some env inherited from host
	env = append(env, inheritHostEnvValues...)

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

	var combinedArgs []string
	if meta.RawCommand {
		// raw command (JSON)
		err = json.Unmarshal([]byte(s.RawCommand()), &combinedArgs)
		if err != nil {
			return err
		}
	} else {
		// TODO look up at runtime
		combinedArgs = []string{os.Getenv("SHELL")}
		if s.RawCommand() != "" {
			combinedArgs = append(combinedArgs, "-c", s.RawCommand())
		}
	}

	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.Env = env
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
	ps, err := cmd.Process.Wait()
	if err != nil {
		return err
	}
	if !ps.Success() {
		return &exec.ExitError{ProcessState: ps}
	}

	return nil
}

func ListenHostSSH(address string) error {
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
