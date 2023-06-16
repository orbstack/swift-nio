package main

import (
	"io"
	"os"
	"runtime"

	"github.com/lxc/go-lxc"
)

type LxcCommand struct {
	CombinedArgs       []string
	Dir                string
	Env                []string
	Stdin              io.Reader
	Stdout             io.Writer
	Stderr             io.Writer
	restrictNamespaces int
	extraFd            int

	Process *os.Process
}

func (c *LxcCommand) Start(container *Container) error {
	namespaces := -1
	if c.restrictNamespaces != 0 {
		namespaces = c.restrictNamespaces
	}

	lxcOpts := lxc.AttachOptions{
		Namespaces: namespaces,
		Arch:       -1,
		Cwd:        c.Dir,
		UID:        0,
		GID:        0,
		Groups:     nil,
		ClearEnv:   true,
		Env:        c.Env,
		EnvToKeep:  nil,
		// filled in below
		StdinFd:            0,
		StdoutFd:           0,
		StderrFd:           0,
		RemountSysProc:     false,
		ElevatedPrivileges: false,
	}

	if file, ok := c.Stdin.(*os.File); ok {
		lxcOpts.StdinFd = uintptr(file.Fd())
		defer runtime.KeepAlive(file)
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		lxcOpts.StdinFd = uintptr(r.Fd())
		defer r.Close()

		// copy stdin to pipe
		go func() {
			_, _ = io.Copy(w, c.Stdin)
			w.Close()
		}()
	}

	if file, ok := c.Stdout.(*os.File); ok {
		lxcOpts.StdoutFd = uintptr(file.Fd())
		defer runtime.KeepAlive(file)
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		lxcOpts.StdoutFd = uintptr(w.Fd())
		defer w.Close()

		// copy pipe to stdout
		go func() {
			_, _ = io.Copy(c.Stdout, r)
			r.Close()
		}()
	}

	if file, ok := c.Stderr.(*os.File); ok {
		lxcOpts.StderrFd = uintptr(file.Fd())
		defer runtime.KeepAlive(file)
	} else {
		// make pipe
		r, w, err := os.Pipe()
		if err != nil {
			return err
		}
		lxcOpts.StderrFd = uintptr(w.Fd())
		defer w.Close()

		// copy pipe to stderr
		go func() {
			_, _ = io.Copy(c.Stderr, r)
			r.Close()
		}()
	}

	childPid, err := container.Exec(c.CombinedArgs, lxcOpts, c.extraFd)
	if err != nil {
		return err
	}

	c.Process, err = os.FindProcess(childPid)
	if err != nil {
		return err
	}

	return nil
}
