package agent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/orbstack/macvirt/scon/agent/envutil"
	"github.com/orbstack/macvirt/scon/util/sysx"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2

	ShellSentinel = "<SHELL>"
)

var haveNixOSLocaleArchive = sync.OnceValue(func() bool {
	_, err := os.Stat("/run/current-system/sw/lib/locale/locale-archive")
	return err == nil
})

type SpawnProcessArgs struct {
	CombinedArgs []string
	Dir          string
	Env          envutil.EnvMap
	User         string
	Setsid       bool
	Setctty      bool
	CttyFd       int
	FdxSeq       uint64

	DoLogin      bool
	ReplaceShell bool
	Argv0        string
}

type SpawnProcessReply struct {
	Pid    int
	FdxSeq uint64
}

type ResolveSSHDirArgs struct {
	User string
	Dir  string
}

func (a *AgentServer) GetAgentPidFd(_ None, reply *uint64) error {
	pidfd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return err
	}
	defer unix.Close(pidfd)

	seq, err := a.fdx.SendFdInt(pidfd)
	if err != nil {
		return err
	}

	*reply = seq
	return nil
}

func (a *AgentServer) ResolveSSHDir(args ResolveSSHDirArgs, reply *string) (err error) {
	cwd := args.Dir
	if cwd == "" {
		u, err := user.Lookup(args.User)
		if err != nil {
			return err
		}
		cwd = u.HomeDir
	}

	// make sure cwd is valid, or exec will fail
	if err := unix.Access(cwd, unix.X_OK); err != nil {
		// reset to / if not
		cwd = "/"
	}

	*reply = cwd
	return nil
}

func lookupShell(user string) (string, error) {
	passwdBytes, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return "", err
	}
	passwd := string(passwdBytes)

	// find user entry
	lines := strings.Split(passwd, "\n")
	for _, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		if fields[0] == user {
			// found
			return fields[6], nil
		}
	}

	return "", errors.New("user not found")
}

func parseShellKvLine(line string) (string, string, bool) {
	// empty line
	if line == "" {
		return "", "", false
	}
	// comment
	if line[0] == '#' {
		return "", "", false
	}
	// shell compat: "export "
	line = strings.TrimPrefix(line, "export ")
	// split kv
	k, v, ok := strings.Cut(line, "=")
	if !ok {
		return "", "", false
	}
	// quotes
	if len(v) > 0 {
		if v[0] == '"' && v[len(v)-1] == '"' {
			v = v[1 : len(v)-1]
		}
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			v = v[1 : len(v)-1]
		}
	}

	return k, v, true
}

func parsePamEnv() ([]string, bool, error) {
	envBytes, err := os.ReadFile("/etc/environment")
	if err != nil {
		return nil, false, err
	}
	env := string(envBytes)

	// parse
	lines := strings.Split(env, "\n")
	envs := make([]string, 0)
	foundPath := false
	for _, line := range lines {
		k, v, ok := parseShellKvLine(line)
		if !ok {
			continue
		}
		envs = append(envs, k+"="+v)
		if k == "PATH" {
			foundPath = true
		}
	}

	return envs, foundPath, nil
}

func (a *AgentServer) SpawnProcess(args SpawnProcessArgs, reply *SpawnProcessReply) error {
	// receive fds
	childFiles, err := a.fdx.RecvFiles(args.FdxSeq)
	// returning sets err
	if err != nil {
		return err
	}
	// any additional fds are passed to the process
	defer closeAllFiles(childFiles)

	pid, pidfd, err := SpawnProcessImpl(a, &args, childFiles)
	if err != nil {
		return err
	}
	defer unix.Close(pidfd)

	// send pidfd
	seq, err := a.fdx.SendFdInt(pidfd)
	if err != nil {
		return fmt.Errorf("send pidfd: %w", err)
	}

	*reply = SpawnProcessReply{
		Pid:    pid,
		FdxSeq: seq,
	}
	return nil
}

func SpawnProcessImpl(a *AgentServer, args *SpawnProcessArgs, childFiles []*os.File) (int, int, error) {
	stdin := childFiles[0]
	stdout := childFiles[1]
	stderr := childFiles[2]

	// resolve the pty, if any
	var ptyF *os.File
	if args.Setctty {
		switch args.CttyFd {
		case fdStdin:
			ptyF = stdin
		case fdStdout:
			ptyF = stdout
		case fdStderr:
			ptyF = stderr
		default:
			return 0, 0, fmt.Errorf("invalid ctty fd: %d", args.CttyFd)
		}
	}

	// create attrs
	attrs := &syscall.SysProcAttr{
		Setsid:  args.Setsid,
		Setctty: args.Setctty,
		Ctty:    args.CttyFd,
	}
	if args.User != "" {
		// get user info
		u, err := user.Lookup(args.User)
		if err != nil {
			return 0, 0, err
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return 0, 0, err
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return 0, 0, err
		}
		groupStrs, err := u.GroupIds()
		if err != nil {
			return 0, 0, err
		}
		groups := make([]uint32, len(groupStrs))
		for i, groupStr := range groupStrs {
			group, err := strconv.Atoi(groupStr)
			if err != nil {
				return 0, 0, err
			}
			groups[i] = uint32(group)
		}

		if args.DoLogin {
			if a == nil {
				return 0, 0, errors.New("DoLogin requires an agent")
			}

			a.loginManager.BeginUserSession(args.User)
			// if start successful, we end after WaitPid
			defer func() {
				if err != nil {
					a.loginManager.EndUserSession(args.User)
				}
			}()
		}

		if args.ReplaceShell && args.CombinedArgs[0] == ShellSentinel {
			// look up user shell
			shell, err := lookupShell(args.User)
			if err != nil {
				return 0, 0, err
			}

			// replace sentinel with shell
			args.CombinedArgs[0] = shell

			if args.DoLogin {
				// replace argv0 with login shell, e.g. -bash
				args.Argv0 = "-" + filepath.Base(shell)

				pamEnv, foundPath, err := parsePamEnv()
				// not exist is ok
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					// never fail though - just log
					logrus.WithError(err).Error("failed to parse /etc/environment")
				}

				// add PAM envs
				for _, pair := range pamEnv {
					args.Env.SetPair(pair)
				}
				if !foundPath {
					// inherit system PATH
					args.Env["PATH"] = os.Getenv("PATH")
				}

				// initial PAM environment
				// set standard login/su environment
				// inherit system PATH
				// https://github.com/util-linux/util-linux/blob/master/login-utils/su-common.c#L760
				args.Env["SHELL"] = shell
				args.Env["HOME"] = u.HomeDir
				args.Env["USER"] = u.Username
				args.Env["LOGNAME"] = u.Username

				// pam_systemd
				// we do enable-linger asynchronously so /run/user/UID won't exist yet,
				// and waiting for it is too slow (~250 ms)
				if _, err := exec.LookPath("loginctl"); err == nil {
					args.Env["XDG_RUNTIME_DIR"] = "/run/user/" + u.Uid
					args.Env["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=/run/user/" + u.Uid + "/bus"
					args.Env["XDG_SESSION_TYPE"] = "tty"
					args.Env["XDG_SESSION_CLASS"] = "user"
				}

				// work around https://github.com/NixOS/nixpkgs/issues/295411 to prevent https://github.com/orbstack/orbstack/issues/1154
				if haveNixOSLocaleArchive() {
					args.Env["LOCALE_ARCHIVE"] = "/run/current-system/sw/lib/locale/locale-archive"
				}
			}
		}

		// if we have a pty (ctty), fix its ownership
		if args.Setctty {
			err = unix.Fchown(int(ptyF.Fd()), uid, gid)
			if err != nil {
				return 0, 0, err
			}
		}

		// doesn't work: permission denied
		attrs.Credential = &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: groups,
		}
	}

	// find the path, and prepend/append our bin to PATH
	if pathValue, ok := args.Env["PATH"]; ok {
		// only add what's not already there
		pathList := strings.Split(pathValue, ":")
		if !slices.Contains(pathList, mounts.BinHiprio) {
			pathList = append([]string{mounts.BinHiprio}, pathList...)
		}
		if !slices.Contains(pathList, mounts.Bin) {
			pathList = append(pathList, mounts.Bin)
		}
		if !slices.Contains(pathList, mounts.UserCmdLinks) {
			pathList = append(pathList, mounts.UserCmdLinks)
		}
		args.Env["PATH"] = strings.Join(pathList, ":")
	}

	// create process
	exePath, err := exec.LookPath(args.CombinedArgs[0])
	if err != nil {
		return 0, 0, err
	}
	if args.Argv0 != "" {
		args.CombinedArgs[0] = args.Argv0
	}
	proc, err := os.StartProcess(exePath, args.CombinedArgs, &os.ProcAttr{
		Dir:   args.Dir,
		Files: childFiles,
		Env:   args.Env.ToPairs(),
		Sys:   attrs,
	})
	if err != nil {
		return 0, 0, err
	}
	defer proc.Release()

	// open pidfd
	pidfd, err := unix.PidfdOpen(proc.Pid, 0)
	if err != nil {
		return 0, 0, err
	}

	return proc.Pid, pidfd, nil
}

func WaitPidImpl(pid int) (int, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0, err
	}

	// wait for process to exit
	ps, err := proc.Wait()
	if err != nil {
		return 0, err
	}

	return ps.ExitCode(), nil
}

func (a *AgentServer) WaitPid(pid int, reply *int) error {
	exitCode, err := WaitPidImpl(pid)
	if err != nil {
		return err
	}

	*reply = exitCode
	return nil
}

func (a *AgentServer) EndUserSession(user string, reply *None) error {
	a.loginManager.EndUserSession(user)
	return nil
}

type AgentCommand struct {
	CombinedArgs []string
	Dir          string
	Env          envutil.EnvMap
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	ExtraFiles   []*os.File
	User         string

	// special login stuff
	DoLogin      bool
	ReplaceShell bool
	Argv0        string // override

	Setsid  bool
	Setctty bool
	CttyFd  int

	Process *PidfdProcess
}

func (c *AgentCommand) prepareStart() ([]*os.File, error) {
	// must always have env map
	if c.Env == nil {
		c.Env = envutil.NewMap()
	}

	var stdin *os.File
	var stdout *os.File
	var stderr *os.File
	if file, ok := c.Stdin.(*os.File); ok {
		stdin = file
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		stdin = r

		// copy stdin to pipe
		go func() {
			io.Copy(w, c.Stdin)
			w.Close()
		}()
	}

	if file, ok := c.Stdout.(*os.File); ok {
		stdout = file
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		stdout = w

		// copy pipe to stdout
		go func() {
			io.Copy(c.Stdout, r)
			r.Close()
		}()
	}

	if file, ok := c.Stderr.(*os.File); ok {
		stderr = file
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		stderr = w

		// copy pipe to stderr
		go func() {
			io.Copy(c.Stderr, r)
			r.Close()
		}()
	}

	// take them all out of nonblock now
	// for some reason, reading from stdin with a big file piped in can return EAGAIN if agent does it in StartProcess
	stdin.Fd()
	stdout.Fd()
	stderr.Fd()

	childFiles := []*os.File{stdin, stdout, stderr}
	childFiles = append(childFiles, c.ExtraFiles...)

	return childFiles, nil
}

func (c *AgentCommand) Start(agent *Client) error {
	childFiles, err := c.prepareStart()
	if err != nil {
		return err
	}
	defer closeAllFiles(childFiles)

	c.Process, err = agent.SpawnProcess(SpawnProcessArgs{
		CombinedArgs: c.CombinedArgs,
		Dir:          c.Dir,
		Env:          c.Env,
		User:         c.User,
		Setsid:       c.Setsid,
		Setctty:      c.Setctty,
		CttyFd:       c.CttyFd,
		DoLogin:      c.DoLogin,
		ReplaceShell: c.ReplaceShell,
		Argv0:        c.Argv0,
	}, childFiles)
	if err != nil {
		return err
	}

	return nil
}

func (c *AgentCommand) StartOnHost() error {
	childFiles, err := c.prepareStart()
	if err != nil {
		return err
	}
	defer closeAllFiles(childFiles)

	pid, pidfd, err := SpawnProcessImpl(nil, &SpawnProcessArgs{
		CombinedArgs: c.CombinedArgs,
		Dir:          c.Dir,
		Env:          c.Env,
		User:         c.User,
		Setsid:       c.Setsid,
		Setctty:      c.Setctty,
		CttyFd:       c.CttyFd,
		DoLogin:      c.DoLogin,
		ReplaceShell: c.ReplaceShell,
		Argv0:        c.Argv0,
	}, childFiles)
	if err != nil {
		return err
	}
	pidF := os.NewFile(uintptr(pidfd), "pidfd")

	c.Process = wrapPidfdProcess(pidF, pid, nil)
	return nil
}

type PidfdProcess struct {
	// not nonblock
	f   *os.File
	pid int
	a   *Client
}

func wrapPidfdProcess(f *os.File, pid int, a *Client) *PidfdProcess {
	return &PidfdProcess{
		f:   f,
		pid: pid,
		a:   a,
	}
}

func (p *PidfdProcess) Release() error {
	return p.f.Close()
}

func (p *PidfdProcess) Close() error {
	return p.Release()
}

func (p *PidfdProcess) Kill() error {
	return p.Signal(os.Kill)
}

func (p *PidfdProcess) Signal(sig os.Signal) error {
	return unix.PidfdSendSignal(int(p.f.Fd()), sig.(unix.Signal), nil, 0)
}

func (p *PidfdProcess) Wait() error {
	return sysx.PollFd(int(p.f.Fd()), unix.POLLIN)
}

func (p *PidfdProcess) WaitStatus() (int, error) {
	// poll first, only call RPC when necessary
	err := p.Wait()
	if err != nil {
		return 0, err
	}

	// call wait to get the status
	// it'll stay a zombie until we do
	var status int
	if p.a == nil {
		status, err = WaitPidImpl(p.pid)
		if err != nil {
			return 0, err
		}
	} else {
		status, err = p.a.WaitPid(p.pid)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) {
				// connection closed, assume process exited with signal
				return -1, nil
			}
			return 0, err
		}
	}

	p.Release()
	return status, nil
}
