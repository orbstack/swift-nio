package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/alessio/shellescape"
	"github.com/creack/pty"
	"github.com/gliderlabs/ssh"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/agent/envutil"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/conf/sshenv"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/termios"
	"github.com/sirupsen/logrus"
	gossh "golang.org/x/crypto/ssh"
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
	hostKeyECDSA = `-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAAAaAAAABNlY2RzYS
1zaGEyLW5pc3RwMjU2AAAACG5pc3RwMjU2AAAAQQSo65hrIeTFpS/ZFiZNzAkPO9zs9GzV
GbZgYtsv8wJ19AgMR8LrYnGNK3cgYVJWnXe5WXjK8IZwxF/jT9cL4YO0AAAAqJDz+WiQ8/
loAAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBKjrmGsh5MWlL9kW
Jk3MCQ873Oz0bNUZtmBi2y/zAnX0CAxHwuticY0rdyBhUladd7lZeMrwhnDEX+NP1wvhg7
QAAAAhALqjXlpenZU0ClqZAG4ypGXwwY0N2/O1uycE8O5Zt7q1AAAACXJvb3RAdWdlbgEC
AwQFBg==
-----END OPENSSH PRIVATE KEY-----`
	hostKeyRSA = `-----BEGIN OPENSSH PRIVATE KEY-----
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
xyXN/213PT8EVPAAAACXJvb3RAdWdlbgE=
-----END OPENSSH PRIVATE KEY-----`
)

const (
	noMachinesMsg = `To use Docker:
    docker run ...
See "orb docker" for more info.

To create a Linux machine:
    orb create ubuntu
See "orb create --help" for supported distros and options.`
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

	sshWormholeSigMap = map[ssh.Signal]os.Signal{
		ssh.SIGABRT: unix.SIGABRT,
		ssh.SIGALRM: unix.SIGALRM,
		ssh.SIGHUP:  unix.SIGHUP,
		ssh.SIGINT:  unix.SIGINT,
		ssh.SIGQUIT: unix.SIGQUIT,
		ssh.SIGTERM: unix.SIGTERM,
		ssh.SIGUSR1: unix.SIGUSR1,
		ssh.SIGUSR2: unix.SIGUSR2,
		ssh.SIGKILL: unix.SIGPWR,
	}

	defaultMeta = sshtypes.SshMeta{
		RawCommand: false,
		PtyStdin:   true,
		PtyStdout:  true,
		PtyStderr:  true,
	}
)

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
				// terminal is in raw mode
				s.Stderr().Write([]byte(strings.Replace(err.Error(), "\n", "\r\n", -1) + "\r\n"))
			}
			s.Exit(1)
		}
	}

	s.Exit(0)
}

func isDefaultUserComp(user string) bool {
	return user == "[default]" || user == "default"
}

func (sv *SshServer) resolveUser(userReq string) (c *Container, user string, isWormhole bool, err error) {
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
		user = "[default]"
		containerName = userParts[0]
	}

	// if we only got one component, and it's not default, try it as a container first
	if !isDefaultUserComp(containerName) {
		c, err = sv.m.GetByName(containerName)
		// if not found, and there was a requested user AND container, bail
		if err != nil && len(userParts) == 2 {
			return
		}
	}

	if c == nil {
		// fallback path: either default container was requested,
		// or we got one component and it wasn't a valid container name, so try it as a user
		c, err = sv.m.GetDefaultContainer()
		if err != nil {
			if errors.Is(err, ErrNoMachines) {
				err = fmt.Errorf("%s\n\n%s", err, noMachinesMsg)
			}
			return
		}
	}

	// default user?
	if isDefaultUserComp(user) {
		user = c.config.DefaultUsername
	}

	if c.ID == ContainerIDDocker && strings.HasPrefix(user, "wormhole:") {
		// wormhole is OK on release builds
		isWormhole = true
		user = user[len("wormhole:"):]
	} else if !conf.Debug() && c.builtin {
		err = fmt.Errorf("cannot enter builtin machine: %s", containerName)
		return
	}

	if !c.Running() {
		logrus.WithFields(logrus.Fields{
			"container": containerName,
		}).Info("starting container for ssh")

		err = c.Start()
		if err != nil {
			return
		}
	}

	return
}

func (sv *SshServer) handleSubsystem(s ssh.Session) (printErr bool, err error) {
	_, _, isPty := s.Pty()
	printErr = isPty

	container, user, isWormhole, err := sv.resolveUser(s.User())
	if err != nil {
		return
	}

	// for dev+docker: keep a freezer ref
	freezer := container.Freezer()
	if freezer != nil {
		freezer.IncRef()
		defer freezer.DecRef()
	}

	// set as last container
	if !container.builtin {
		go sv.m.db.SetLastContainerID(container.ID)
	}

	// ok, container is up, now handle the request
	switch s.Subsystem() {
	case "session", "":
		return sv.handleCommandSession(s, container, user, isWormhole)
	case "sftp":
		if isWormhole {
			err = fmt.Errorf("sftp not supported with wormhole")
			return
		}

		return false, sv.handleSftp(s, container, user)
	default:
		err = fmt.Errorf("unknown subsystem: %s", s.Subsystem())
		return
	}
}

func (sv *SshServer) handleCommandSession(s ssh.Session, container *Container, user string, isWormhole bool) (bool, error) {
	ptyReq, winCh, isPty := s.Pty()
	printErr := isPty

	// new empty env (agent adds basics)
	env := envutil.NewMap()

	// ssh env: extract __ORB_META metadata, and add anything client sent
	var meta sshtypes.SshMeta
	for _, kv := range s.Environ() {
		env.SetPair(kv)
	}
	if metaStr, ok := env[sshtypes.KeyMeta]; ok {
		err := json.Unmarshal([]byte(metaStr), &meta)
		if err != nil {
			return printErr, err
		}
		delete(env, sshtypes.KeyMeta)
	} else {
		meta = defaultMeta
		meta.PtyStdin = isPty
		meta.PtyStdout = isPty
		meta.PtyStderr = isPty
	}

	logrus.WithFields(logrus.Fields{
		"pty":  isPty,
		"user": s.User(),
		"cmd":  s.RawCommand(),
		"meta": meta,
	}).Debug("SSH connection - command session")

	var wormholeTarget string
	if isWormhole {
		wormholeTarget = user
		user = "root"

		// check for Pro license
		if !sv.m.drm.isLicensed() {
			return printErr, &ExitError{status: sshenv.ExitCodeNeedsProLicense}
		}
	}

	// pwd
	cwd, err := UseAgentRet(container, func(a *agent.Client) (string, error) {
		return a.ResolveSSHDir(agent.ResolveSSHDirArgs{
			User: user,
			Dir:  meta.Pwd,
		})
	})
	if err != nil {
		return printErr, err
	}

	// env: set TERM and PWD
	if isPty {
		env["TERM"] = ptyReq.Term
	}
	env["PWD"] = cwd
	// set prompt ssh
	env["SSH_CONNECTION"] = "::1 0 ::1 22"

	// forward ssh agent
	sshAgentSocks, err := sv.m.host.GetSSHAgentSockets()
	if err != nil {
		return printErr, err
	}
	if sshAgentSocks.Preferred != "" {
		env["SSH_AUTH_SOCK"] = mounts.SshAgentSocket
	}

	// set up xdg-open to use macOS open
	// this will only work when users don't have a desktop environment: https://gitlab.freedesktop.org/xdg/xdg-utils/-/blob/0f6385262417f1c0c4d13bc05d95c32578272b64/scripts/xdg-open.in#L477
	// but i don't think that's a common enough case to care about for now
	env["BROWSER"] = mounts.Bin + "/open"

	cmd := &agent.AgentCommand{
		Env:          env,
		Dir:          cwd,
		User:         user,
		DoLogin:      true,
		ReplaceShell: true,
	}

	if isPty {
		ptyF, ttyF, err := container.OpenPty()
		if err != nil {
			return printErr, err
		}
		// acts as keepalive
		defer ptyF.Close()
		defer ttyF.Close()

		// set size
		err = pty.Setsize(ptyF, &pty.Winsize{
			Rows: uint16(ptyReq.Window.Height),
			Cols: uint16(ptyReq.Window.Width),
		})
		if err != nil {
			return printErr, err
		}

		// set term modes
		tflags, err := termios.GetTermios(ptyF.Fd())
		if err != nil {
			return printErr, err
		}
		termios.ApplySSHToTermios(ptyReq.TerminalModes, tflags)
		err = termios.SetTermiosNow(ptyF.Fd(), tflags)
		if err != nil {
			return printErr, err
		}

		go func() {
			for win := range winCh {
				err := pty.Setsize(ptyF, &pty.Winsize{
					Rows: uint16(win.Height),
					Cols: uint16(win.Width),
				})
				if err != nil {
					logrus.WithError(err).Error("set pty size failed")
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
	var shellCmd string
	if meta.RawCommand {
		// raw command (JSON)
		var rawArgs []string
		err = json.Unmarshal([]byte(s.RawCommand()), &rawArgs)
		if err != nil {
			return printErr, err
		}
		// still go through shell to get PATH
		shellCmd = shellescape.QuoteCommand(rawArgs)
		combinedArgs = []string{agent.ShellSentinel, "-c", shellCmd}
	} else {
		combinedArgs = []string{agent.ShellSentinel}
		if s.RawCommand() != "" {
			shellCmd = s.RawCommand()
			combinedArgs = append(combinedArgs, "-c", shellCmd)
		}
	}
	cmd.CombinedArgs = combinedArgs

	if isWormhole {
		return sv.handleWormhole(s, cmd, container, wormholeTarget, shellCmd, &meta, isPty)
	}

	err = container.UseAgent(func(a *agent.Client) error {
		return cmd.Start(a)
	})
	if err != nil {
		return printErr, err
	}
	defer func() {
		err := container.UseAgent(func(a *agent.Client) error {
			return a.EndUserSession(user)
		})
		if err != nil && !errors.Is(err, ErrMachineNotRunning) {
			logrus.WithError(err).Error("end user session failed")
		}
	}()

	// now that the command has been started, don't print errors to pty
	printErr = false

	// kill process if session is closed
	go func() {
		<-s.Context().Done()
		_ = cmd.Process.Kill()
	}()

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
			sig, ok := sshSigMap[sshSig]
			if !ok {
				continue
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
	status, err := cmd.Process.WaitStatus()
	if err != nil {
		logrus.Error("wait failed: ", err)
		return printErr, err
	}

	if status != 0 {
		return printErr, &ExitError{status: status}
	}

	return printErr, nil
}

func (sv *SshServer) handleSftp(s ssh.Session, container *Container, user string) error {
	// make socketpair
	socketFds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return err
	}

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
		_, _ = io.Copy(s, conn0)
	}()
	go func() {
		defer conn0.Close()
		_, _ = io.Copy(conn0, s)
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

// OpenSSH extension: streamlocal forward for Unix sockets
// https://github.com/openssh/openssh-portable/blob/05f2b141cfcc60c7cdedf9450d2b9d390c19eaad/PROTOCOL#L322
type streamLocalChannelData struct {
	SocketPath string
	Reserved1  string
	Reserved2  uint32
}

func (sv *SshServer) finishProxyChannelOpen(ctx ssh.Context, newChan gossh.NewChannel, network string, addrPort string) {
	container, _, isWormhole, err := sv.resolveUser(ctx.User())
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, err.Error())
		return
	}
	if isWormhole {
		newChan.Reject(gossh.ConnectionFailed, "can't forward to wormhole")
		return
	}

	dstConn, err := UseAgentRet(container, func(a *agent.Client) (net.Conn, error) {
		return a.DialContext(network, addrPort)
	})
	if err != nil {
		newChan.Reject(gossh.ConnectionFailed, err.Error())
		return
	}

	sshCh, reqs, err := newChan.Accept()
	if err != nil {
		logrus.WithError(err).Error("accept failed")
		dstConn.Close()
		return
	}
	go gossh.DiscardRequests(reqs)

	go func() {
		defer sshCh.Close()
		defer dstConn.Close()
		_, _ = io.Copy(sshCh, dstConn)
	}()
	go func() {
		defer sshCh.Close()
		defer dstConn.Close()
		_, _ = io.Copy(dstConn, sshCh)
	}()
}

func (sv *SshServer) handleLocalForward(srv *ssh.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
	d := localForwardChannelData{}
	if err := gossh.Unmarshal(newChan.ExtraData(), &d); err != nil {
		newChan.Reject(gossh.ConnectionFailed, "parse tcpip forward data: "+err.Error())
		return
	}

	dest := net.JoinHostPort(d.DestAddr, strconv.FormatInt(int64(d.DestPort), 10))
	sv.finishProxyChannelOpen(ctx, newChan, "tcp", dest)
}

func (sv *SshServer) handleStreamLocalForward(srv *ssh.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
	d := streamLocalChannelData{}
	if err := gossh.Unmarshal(newChan.ExtraData(), &d); err != nil {
		newChan.Reject(gossh.ConnectionFailed, "parse stream local forward data: "+err.Error())
		return
	}

	sv.finishProxyChannelOpen(ctx, newChan, "unix", d.SocketPath)
}

func (m *ConManager) runSSHServer(listenIP4, listenIP6 string) (func() error, error) {
	listenerInternal, err := netx.Listen("tcp", net.JoinHostPort(listenIP4, strconv.Itoa(ports.GuestSconSSH)))
	if err != nil {
		return nil, err
	}

	listenerPublic4, err := netx.Listen("tcp4", net.JoinHostPort(listenIP4, strconv.Itoa(ports.GuestSconSSHPublic)))
	if err != nil {
		return nil, err
	}

	listenerPublic6, err := netx.Listen("tcp6", net.JoinHostPort(listenIP6, strconv.Itoa(ports.GuestSconSSHPublic)))
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
			Version: "OrbStack",
			ChannelHandlers: map[string]ssh.ChannelHandler{
				"session": ssh.DefaultSessionHandler,
			},
			SubsystemHandlers: make(map[string]ssh.SubsystemHandler),
			RequestHandlers:   make(map[string]ssh.RequestHandler),
			ConnectionFailedCallback: func(conn net.Conn, err error) {
				logrus.WithError(err).Error("SSH connection failed")
			},
		},
	}
	sshServerPub.Handler = sshServerPub.handleConn
	sshServerPub.SubsystemHandlers["sftp"] = sshServerPub.handleConn
	sshServerPub.ChannelHandlers["direct-tcpip"] = sshServerPub.handleLocalForward
	sshServerPub.ChannelHandlers["direct-streamlocal@openssh.com"] = sshServerPub.handleStreamLocalForward
	sshServerPub.SetOption(ssh.HostKeyPEM([]byte(hostKeyEd25519)))
	sshServerPub.SetOption(ssh.HostKeyPEM([]byte(hostKeyECDSA)))
	sshServerPub.SetOption(ssh.HostKeyPEM([]byte(hostKeyRSA)))

	go runOne("internal SSH server", func() error {
		err := sshServerInt.Serve(listenerInternal)
		if err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			return err
		}
		return nil
	})

	pubKeysStr, err := m.host.GetSSHAuthorizedKeys()
	if err != nil {
		return nil, err
	}

	// parse all authorized keys
	var pubKeys []ssh.PublicKey
	for _, pubKeyStr := range strings.Split(pubKeysStr, "\n") {
		// skip comments and empty lines
		if strings.HasPrefix(pubKeyStr, "#") || pubKeyStr == "" {
			continue
		}

		pubKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubKeyStr))
		if err != nil {
			logrus.WithError(err).WithField("key", pubKeyStr).Error("invalid SSH authorized key")
		}

		// dedupe
		found := false
		for _, existing := range pubKeys {
			if ssh.KeysEqual(existing, pubKey) {
				found = true
				break
			}
		}
		if !found {
			pubKeys = append(pubKeys, pubKey)
		}
	}

	pubKeyOpt := ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
		for _, pubKey := range pubKeys {
			if ssh.KeysEqual(key, pubKey) {
				return true
			}
		}
		return false
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
