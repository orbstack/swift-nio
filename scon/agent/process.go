package agent

import (
	"io"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

const (
	fdStdin  = 0
	fdStdout = 1
	fdStderr = 2
)

type SpawnProcessArgs struct {
	CombinedArgs []string
	Dir          string
	Env          []string
	Setsid       bool
	Setctty      bool
	CttyFd       int
}

func (a *AgentServer) SpawnProcess(args *SpawnProcessArgs, reply *None) error {
	// receive fds
	stdin, err := a.fdx.RecvFile()
	if err != nil {
		return err
	}
	defer stdin.Close()

	stdout, err := a.fdx.RecvFile()
	if err != nil {
		return err
	}
	defer stdout.Close()

	stderr, err := a.fdx.RecvFile()
	if err != nil {
		return err
	}
	defer stderr.Close()

	// create process
	path := args.CombinedArgs[0]
	proc, err := os.StartProcess(path, args.CombinedArgs, &os.ProcAttr{
		Dir:   args.Dir,
		Files: []*os.File{stdin, stdout, stderr},
		Env:   args.Env,
		Sys: &syscall.SysProcAttr{
			Setsid:  args.Setsid,
			Setctty: args.Setctty,
			Ctty:    args.CttyFd,
		},
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
	err = a.fdx.SendFdInt(pidfd)
	if err != nil {
		return err
	}

	return nil
}

type AgentCommand struct {
	CombinedArgs []string
	Dir          string
	Env          []string
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer

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
		Setsid:       c.Setsid,
		Setctty:      c.Setctty,
		CttyFd:       c.CttyFd,
	}, stdin, stdout, stderr)
	if err != nil {
		return err
	}

	return nil
}

type PidfdProcess struct {
	f *os.File
}

func wrapPidfdProcess(f *os.File) *PidfdProcess {
	return &PidfdProcess{f: f}
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

func (p *PidfdProcess) Wait() (*os.ProcessState, error) {
	var info unix.Siginfo
	err := unix.Waitid(unix.P_PIDFD, int(p.f.Fd()), &info, unix.WEXITED, nil)
	if err != nil {
		return nil, err
	}

	return &os.ProcessState{}, nil
}
