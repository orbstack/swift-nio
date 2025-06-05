package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/orbstack/macvirt/scon/util/dirfs"
	"golang.org/x/sys/unix"
)

type ContainerRuntimeState struct {
	// TODO[6.15pidfd]: remove all usages of this
	InitPid int

	// the man pages lie: /proc/pid dirfd is *not* the same as pidfd for all purpose other than poll/waitid.
	// anything in the kernel that calls pidfd_pid() instead of pidfd_to_pid() only works on real pidfds, including setns.
	InitPidfd *os.File

	InitProcDirfd *dirfs.FS
}

func (c *Container) RuntimeState() (*ContainerRuntimeState, error) {
	rt := c.runtimeState.Load()
	if rt == nil {
		return nil, ErrMachineNotRunning
	}
	return rt, nil
}

func (c *Container) initRuntimeStateLocked() error {
	initPid := c.lxc.InitPid()
	if initPid == -1 {
		return errors.New("init exited")
	}

	initPidfd, err := c.lxc.InitPidFd()
	if err != nil {
		return fmt.Errorf("get init pidfd: %w", err)
	}

	initProcDirfd, err := os.OpenFile("/proc/"+strconv.Itoa(initPid), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return fmt.Errorf("open proc dir: %w", err)
	}

	initProcDirfdFs, err := dirfs.NewFromDirfd(initProcDirfd)
	if err != nil {
		return fmt.Errorf("create fs from dirfd: %w", err)
	}

	rt := &ContainerRuntimeState{
		InitPid:       initPid,
		InitPidfd:     initPidfd,
		InitProcDirfd: initProcDirfdFs,
	}
	c.runtimeState.Store(rt)

	return nil
}

func (r *ContainerRuntimeState) Close() error {
	if r.InitPidfd != nil {
		r.InitPidfd.Close()
	}
	if r.InitProcDirfd != nil {
		r.InitProcDirfd.Close()
	}
	return nil
}
