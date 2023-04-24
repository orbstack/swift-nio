package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/conf/sshenv"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/sshtypes"
	"github.com/kdrag0n/macvirt/macvmgr/vnet/services/hostssh/termios"
	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/sirupsen/logrus"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/exp/slices"
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

func translateProxyEnv(key, value string) (string, error) {
	// translate proxy url
	u, err := url.Parse(value)
	if err != nil {
		return "", err
	}

	// split host:port
	host, port, err := net.SplitHostPort(u.Host)
	if addrError, ok := err.(*net.AddrError); ok && addrError.Err == "missing port in address" {
		// no port, use default
		host = u.Host
	} else if err != nil {
		return "", err
	}

	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		if port == "" {
			u.Host = "host.orb.internal"
		} else {
			u.Host = "host.orb.internal:" + port
		}

		return key + "=" + u.String(), nil
	}

	return key + "=" + value, nil
}

// filter out sshenv exclusions and translate proxies
func translateEnvs(env []string) []string {
	filtered := make([]string, 0, len(env))

	for _, kv := range env {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}

		if slices.Contains(sshenv.ProxyEnvs, key) {
			// translate proxy env
			translated, err := translateProxyEnv(key, value)
			if err != nil {
				logrus.WithError(err).WithField("env", kv).Warn("Failed to translate proxy env")
				filtered = append(filtered, kv)
				continue
			}

			filtered = append(filtered, translated)
		} else {
			filtered = append(filtered, kv)
		}
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
	*ssh.Server
	m *ConManager
}

func (sv *SshServer) handleConn(s ssh.Session) {
	defer s.Close()

	printErr, err := sv.handleSubsystem(s)
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

func (sv *SshServer) resolveUser(userReq string) (container *Container, user string, err error) {
	// user and container
	userParts := strings.Split(userReq, "@")
	if len(userParts) > 2 {
		err = fmt.Errorf("invalid user: %s", userReq)
		return
	}
	var containerName string
	if len(userParts) == 2 {
		user = userParts[0]
		containerName = userParts[1]
	} else {
		// default user = host user
		user, err = sv.m.defaultUser()
		if err != nil {
			return
		}
		containerName = userParts[0]
	}

	// default container?
	defaultContainerObj, err := sv.m.GetDefaultContainer()
	if err != nil {
		return
	}
	defaultContainer := defaultContainerObj.Name
	if containerName == "default" {
		containerName = defaultContainer
	}

	// default user?
	if user == "[default]" {
		user, err = sv.m.defaultUser()
		if err != nil {
			return
		}
	}

	container, ok := sv.m.GetByName(containerName)
	// try default container
	if !ok && len(userParts) == 1 {
		container, ok = sv.m.GetByName(defaultContainer)
		if ok {
			containerName = defaultContainer
			user = userParts[0]
		}
	}
	if !ok {
		err = fmt.Errorf("machine not found: %s", containerName)
		return
	}

	if !conf.Debug() && container.builtin {
		err = fmt.Errorf("cannot enter builtin machine: %s", containerName)
		return
	}

	if !container.Running() {
		logrus.WithFields(logrus.Fields{
			"container": containerName,
		}).Info("starting container for ssh")

		err = container.Start()
		if err != nil {
			return
		}
	}

	return
}

func (sv *SshServer) handleSubsystem(s ssh.Session) (printErr bool, err error) {
	_, _, isPty := s.Pty()
	printErr = isPty

	container, user, err := sv.resolveUser(s.User())
	if err != nil {
		return
	}

	// set as last container
	if !container.builtin {
		go sv.m.db.SetLastContainerID(container.ID)
	}

	// ok, container is up, now handle the request
	switch s.Subsystem() {
	case "session", "":
		return sv.handleCommandSession(s, container, user)
	case "sftp":
		return false, sv.handleSftp(s, container, user)
	default:
		err = fmt.Errorf("unknown subsystem: %s", s.Subsystem())
		return
	}
}

func (sv *SshServer) handleCommandSession(s ssh.Session, container *Container, user string) (printErr bool, err error) {
	ptyReq, winCh, isPty := s.Pty()
	printErr = isPty

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
	}).Debug("SSH connection - command session")

	// translate proxy envs
	env = translateEnvs(env)

	// pwd
	cwd, err := UseAgentRet(container, func(a *agent.Client) (string, error) {
		return a.ResolveSSHDir(agent.ResolveSSHDirArgs{
			User: user,
			Dir:  meta.Pwd,
		})
	})
	if err != nil {
		return
	}

	// env: set TERM and PWD
	if isPty {
		env = append(env, "TERM="+ptyReq.Term)
	}
	env = append(env, "PWD="+cwd)
	// set prompt ssh
	env = append(env, "SSH_CONNECTION=::1 0 ::1 22")

	// forward ssh agent
	sshAgentSocks, err := sv.m.host.GetSSHAgentSockets()
	if err != nil {
		return
	}
	if sshAgentSocks.Preferred != "" {
		env = append(env, "SSH_AUTH_SOCK="+mounts.SshAgentSocket)
	}

	cmd := &agent.AgentCommand{
		Env:          env,
		Dir:          cwd,
		User:         user,
		DoLogin:      true,
		ReplaceShell: true,
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

	// after stdin/stdout setup, to deal with nixos su
	var combinedArgs []string
	if meta.RawCommand {
		// raw command (JSON)
		var rawArgs []string
		err = json.Unmarshal([]byte(s.RawCommand()), &rawArgs)
		if err != nil {
			return
		}
		// still go through shell to get PATH
		combinedArgs = []string{agent.ShellSentinel, "-c", shellescape.QuoteCommand(rawArgs)}
	} else {
		combinedArgs = []string{agent.ShellSentinel}
		if s.RawCommand() != "" {
			combinedArgs = append(combinedArgs, "-c", s.RawCommand())
		}
	}
	cmd.CombinedArgs = combinedArgs

	err = container.UseAgent(func(a *agent.Client) error {
		return cmd.Start(a)
	})
	if err != nil {
		return
	}
	defer func() {
		if !container.Running() {
			return
		}

		err := container.UseAgent(func(a *agent.Client) error {
			return a.EndUserSession(user)
		})
		if err != nil {
			logrus.WithError(err).Error("end user session failed")
		}
	}()

	// now that the command has been started, don't print errors to pty
	printErr = false

	// forward signals
	fwdSigChan := make(chan ssh.Signal, 1)
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

func (sv *SshServer) handleSftp(s ssh.Session, container *Container, user string) error {
	// make socketpair
	socketFds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}

	// make socketpair nonblocking
	unix.SetNonblock(socketFds[0], true)
	unix.SetNonblock(socketFds[1], true)

	// wrap them in files
	socketF0 := os.NewFile(uintptr(socketFds[0]), "sftp-socket0")
	socketF1 := os.NewFile(uintptr(socketFds[1]), "sftp-socket1")
	defer socketF1.Close()
	conn0, err := net.FileConn(socketF0)
	socketF0.Close() // otherwise fd will keep conn alive after EOF
	if err != nil {
		return err
	}

	// will cause sftp server to exit
	go func() {
		defer conn0.Close()
		io.Copy(s, conn0)
	}()
	go func() {
		defer conn0.Close()
		io.Copy(conn0, s)
	}()

	exitCode, err := UseAgentRet(container, func(a *agent.Client) (int, error) {
		return a.ServeSftp(user, socketF1)
	})
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return &ExitError{status: exitCode}
	}

	return nil
}

// direct-tcpip data struct as specified in RFC4254, Section 7.2
type localForwardChannelData struct {
	DestAddr string
	DestPort uint32

	OriginAddr string
	OriginPort uint32
}

func (sv *SshServer) handleLocalForward(srv *ssh.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
	d := localForwardChannelData{}
	if err := gossh.Unmarshal(newChan.ExtraData(), &d); err != nil {
		newChan.Reject(gossh.ConnectionFailed, "parse forward data: "+err.Error())
		return
	}

	dest := net.JoinHostPort(d.DestAddr, strconv.FormatInt(int64(d.DestPort), 10))

	container, _, err := sv.resolveUser(ctx.User())
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, err.Error())
		return
	}

	dstConn, err := UseAgentRet(container, func(a *agent.Client) (net.Conn, error) {
		return a.DialTCPContext(dest)
	})
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, err.Error())
		return
	}

	sshCh, reqs, err := newChan.Accept()
	if err != nil {
		dstConn.Close()
		return
	}
	go gossh.DiscardRequests(reqs)

	go func() {
		defer sshCh.Close()
		defer dstConn.Close()
		io.Copy(sshCh, dstConn)
	}()
	go func() {
		defer sshCh.Close()
		defer dstConn.Close()
		io.Copy(dstConn, sshCh)
	}()
}

func (m *ConManager) runSSHServer(listenIP4, listenIP6 string) (func() error, error) {
	listenerInternal, err := net.Listen("tcp", net.JoinHostPort(listenIP4, strconv.Itoa(ports.GuestSconSSH)))
	if err != nil {
		return nil, err
	}

	listenerPublic4, err := net.Listen("tcp4", net.JoinHostPort(listenIP4, strconv.Itoa(ports.GuestSconSSHPublic)))
	if err != nil {
		return nil, err
	}

	listenerPublic6, err := net.Listen("tcp6", net.JoinHostPort(listenIP6, strconv.Itoa(ports.GuestSconSSHPublic)))
	if err != nil {
		return nil, err
	}

	sshServerInt := &SshServer{
		m:      m,
		Server: &ssh.Server{},
	}
	sshServerInt.Handler = sshServerInt.handleConn
	sshServerInt.SetOption(ssh.HostKeyPEM([]byte(hostKeyEd25519)))

	// public supports SFTP, local forward
	sshServerPub := &SshServer{
		m: m,
		Server: &ssh.Server{
			ChannelHandlers: map[string]ssh.ChannelHandler{
				"session": ssh.DefaultSessionHandler,
			},
			SubsystemHandlers: make(map[string]ssh.SubsystemHandler),
			RequestHandlers:   make(map[string]ssh.RequestHandler),
		},
	}
	sshServerPub.Handler = sshServerPub.handleConn
	sshServerPub.SubsystemHandlers["sftp"] = sshServerPub.handleConn
	sshServerPub.ChannelHandlers["direct-tcpip"] = sshServerPub.handleLocalForward
	sshServerPub.SetOption(ssh.HostKeyPEM([]byte(hostKeyEd25519)))

	go runOne("internal SSH server", func() error {
		err := sshServerInt.Serve(listenerInternal)
		if err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			return err
		}
		return nil
	})

	pubKeyStr, err := m.host.GetSSHPublicKey()
	if err != nil {
		return nil, err
	}
	pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKeyStr))
	if err != nil {
		return nil, err
	}

	pubKeyOpt := ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
		return ssh.KeysEqual(key, pubKey)
	})
	sshServerPub.SetOption(pubKeyOpt)
	go runOne("public SSH server v4", func() error {
		err := sshServerPub.Serve(listenerPublic4)
		if err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			return err
		}
		return nil
	})
	go runOne("public SSH server v6", func() error {
		err := sshServerPub.Serve(listenerPublic6)
		if err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			return err
		}
		return nil
	})

	// cleanup func
	return func() error {
		sshServerInt.Close()
		sshServerPub.Close()
		return nil
	}, nil
}
