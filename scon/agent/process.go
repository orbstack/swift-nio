package agent

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"github.com/kdrag0n/macvirt/scon/agent/envutil"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2

	ShellSentinel = "<SHELL>"
)

type SpawnProcessArgs struct {
	CombinedArgs []string
	Dir          string
	Env          []string
	User         string
	Setsid       bool
	Setctty      bool
	CttyFd       int
	FdxSeq       uint64

	DoLogin      bool
	ReplaceShell bool
}

type SpawnProcessReply struct {
	Pid    int
	FdxSeq uint64
}

type ResolveSSHDirArgs struct {
	User string
	Dir  string
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
		// empty line
		if line == "" {
			continue
		}
		// comment
		if line[0] == '#' {
			continue
		}
		// shell compat: "export "
		line = strings.TrimPrefix(line, "export ")
		// split kv
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
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
		envs = append(envs, k+"="+v)
		if k == "PATH" {
			foundPath = true
		}
	}

	return envs, foundPath, nil
}

func (a *AgentServer) SpawnProcess(args SpawnProcessArgs, reply *SpawnProcessReply) (err error) {
	// receive fds
	stdios, err := a.fdx.RecvFiles(args.FdxSeq)
	// returning sets err
	if err != nil {
		return err
	}
	stdin := stdios[0]
	stdout := stdios[1]
	stderr := stdios[2]
	defer stdin.Close()
	defer stdout.Close()
	defer stderr.Close()

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
			return fmt.Errorf("invalid ctty fd: %d", args.CttyFd)
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
			return err
		}
		uid, err := strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		gid, err := strconv.Atoi(u.Gid)
		if err != nil {
			return err
		}
		groupStrs, err := u.GroupIds()
		if err != nil {
			return err
		}
		groups := make([]uint32, len(groupStrs))
		for i, groupStr := range groupStrs {
			group, err := strconv.Atoi(groupStr)
			if err != nil {
				return err
			}
			groups[i] = uint32(group)
		}

		if args.DoLogin {
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
				return err
			}

			// replace sentinel with shell
			args.CombinedArgs[0] = shell

			if args.DoLogin {
				pamEnv, foundPath, err := parsePamEnv()
				// not exist is ok
				if err != nil && !errors.Is(err, os.ErrNotExist) {
					// never fail though - just log
					logrus.WithError(err).Error("failed to parse /etc/environment")
				}

				args.Env = append(args.Env, pamEnv...)
				if !foundPath {
					// inherit system PATH
					args.Env = append(args.Env, "PATH="+os.Getenv("PATH"))
				}

				// initial PAM environment
				// set standard login/su environment
				// inherit system PATH
				// https://github.com/util-linux/util-linux/blob/master/login-utils/su-common.c#L760
				args.Env = append(args.Env,
					"SHELL="+shell,
					"HOME="+u.HomeDir,
					"USER="+u.Username,
					"LOGNAME="+u.Username,
				)

				// pam_systemd
				// we do enable-linger asynchronously so /run/user/UID won't exist yet,
				// and waiting for it is too slow (~250 ms)
				if _, err := exec.LookPath("loginctl"); err == nil {
					args.Env = append(args.Env,
						"XDG_RUNTIME_DIR=/run/user/"+u.Uid,
						"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+u.Uid+"/bus",
						"XDG_SESSION_TYPE=tty",
						"XDG_SESSION_CLASS=user",
					)
				}
			}
		}

		// if we have a pty (ctty), fix its ownership
		if args.Setctty {
			err = unix.Fchown(int(ptyF.Fd()), uid, gid)
			if err != nil {
				return err
			}
		}

		// doesn't work: permission denied
		attrs.Credential = &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: groups,
		}
	}

	// dedupe env
	args.Env = envutil.Dedupe(args.Env)

	// create process
	exePath, err := exec.LookPath(args.CombinedArgs[0])
	if err != nil {
		return err
	}
	proc, err := os.StartProcess(exePath, args.CombinedArgs, &os.ProcAttr{
		Dir:   args.Dir,
		Files: []*os.File{stdin, stdout, stderr},
		Env:   args.Env,
		Sys:   attrs,
	})
	if err != nil {
		return err
	}
	defer proc.Release()

	// open pidfd
	pidfd, err := unix.PidfdOpen(proc.Pid, 0)
	if err != nil {
		return err
	}
	defer unix.Close(pidfd)

	// send pidfd
	seq, err := a.fdx.SendFdInt(pidfd)
	if err != nil {
		return err
	}

	*reply = SpawnProcessReply{
		Pid:    proc.Pid,
		FdxSeq: seq,
	}
	return nil
}

func (a *AgentServer) WaitPid(pid int, reply *int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	// wait for process to exit
	ps, err := proc.Wait()
	if err != nil {
		return err
	}

	*reply = ps.ExitCode()
	return nil
}

func (a *AgentServer) EndUserSession(user string, reply *None) error {
	a.loginManager.EndUserSession(user)
	return nil
}

type AgentCommand struct {
	CombinedArgs []string
	Dir          string
	Env          []string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	User         string

	// special login stuff
	DoLogin      bool
	ReplaceShell bool

	Setsid  bool
	Setctty bool
	CttyFd  int

	Process *PidfdProcess
}

func (c *AgentCommand) Start(agent *Client) error {
	var stdin *os.File
	var stdout *os.File
	var stderr *os.File
	if file, ok := c.Stdin.(*os.File); ok {
		stdin = file
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		stdin = r
		defer r.Close()

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
			return err
		}
		stdout = w
		defer w.Close()

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
			return err
		}
		stderr = w
		defer w.Close()

		// copy pipe to stderr
		go func() {
			io.Copy(c.Stderr, r)
			r.Close()
		}()
	}

	var err error
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
	}, stdin, stdout, stderr)
	if err != nil {
		return err
	}

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

func (p *PidfdProcess) Wait() (int, error) {
	// poll first, only call RPC when necessary
	for {
		fds := [1]unix.PollFd{
			{
				Fd:     int32(p.f.Fd()),
				Events: unix.POLLIN,
			},
		}
		_, err := unix.Poll(fds[:], 0)
		if err == nil {
			break
		}
		if err == unix.EINTR {
			continue
		}
		return 0, err
	}

	// call wait to get the status
	// it'll stay a zombie until we do
	status, err := p.a.WaitPid(p.pid)
	if err != nil {
		if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrUnexpectedEOF) {
			// connection closed, assume process exited with signal
			return -1, nil
		}
		return 0, err
	}

	p.Release()
	return status, nil
}
