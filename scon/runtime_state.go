package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/util/dirfs"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type ContainerRuntimeState struct {
	// --- all must be populated when rt is made visible --
	cgroupPath string

	// TODO[6.15pidfd]: remove all usages of this
	InitPid int
	// the man pages lie: /proc/pid dirfd is *not* the same as pidfd for all purpose other than poll/waitid.
	// anything in the kernel that calls pidfd_pid() instead of pidfd_to_pid() only works on real pidfds, including setns.
	InitPidfd     *os.File
	InitProcDirfd *dirfs.FS

	bpf   *bpf.ContainerBpfManager
	agent *agent.Client
	// docker
	freezer *Freezer
	// --- end ---

	// --- dynamic state ---

	// protected by listenersDebounce mutex
	listenersDebounce  *syncx.FuncDebounce
	listeners          []sysnet.ListenerInfo
	activeForwards     map[sysnet.ListenerKey]struct{}
	listenerDirtyFlags atomic.Uint32

	ipAddrsMu syncx.Mutex
	ipAddrs   []net.IP
}

func (c *Container) RuntimeState() (*ContainerRuntimeState, error) {
	rt := c.runtimeState.Load()
	if rt == nil {
		return nil, ErrMachineNotRunning
	}
	return rt, nil
}

func (c *Container) initRuntimeStateLocked(cgroupPath string) (_ *ContainerRuntimeState, retErr error) {
	initPid := c.lxc.InitPid()
	if initPid == -1 {
		return nil, errors.New("init exited")
	}

	initPidfd, err := c.lxc.InitPidFd()
	if err != nil {
		return nil, fmt.Errorf("get init pidfd: %w", err)
	}
	defer func() {
		if retErr != nil {
			initPidfd.Close()
		}
	}()

	initProcDirfd, err := os.OpenFile("/proc/"+strconv.Itoa(initPid), os.O_RDONLY|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, fmt.Errorf("open proc dir: %w", err)
	}
	initProcDirfdFs, err := dirfs.NewFromDirfd(initProcDirfd)
	if err != nil {
		return nil, fmt.Errorf("create fs from dirfd: %w", err)
	}
	defer func() {
		if retErr != nil {
			initProcDirfdFs.Close()
		}
	}()

	rt := &ContainerRuntimeState{
		InitPid:        initPid,
		InitPidfd:      initPidfd,
		InitProcDirfd:  initProcDirfdFs,
		cgroupPath:     cgroupPath,
		activeForwards: make(map[sysnet.ListenerKey]struct{}),
	}
	rt.listenersDebounce = syncx.NewFuncDebounce(autoForwardDebounce, func() {
		err := rt.updateListenersNow(c)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to update listeners")
		}
	})
	// this covers all cleanups below, as we start adding each thing to rt
	defer func() {
		if retErr != nil {
			rt.Close()
		}
	}()

	// must happen immediately after creating rt, because everything assumes that docker machine's rt always has freezer != nil
	rt.freezer, err = c.hooks.MakeFreezer(c, rt)
	if err != nil {
		return nil, fmt.Errorf("make freezer: %w", err)
	}

	rt.agent, err = c.startAgentLocked()
	if err != nil {
		logrus.WithError(err).WithField("container", c.Name).Error("failed to start agent")
	}

	rt.bpf, err = c.attachBpf(rt)
	if err != nil {
		return nil, fmt.Errorf("attach bpf: %w", err)
	}

	// when rt is published, everything is filled in
	oldRt := c.runtimeState.Swap(rt)
	if oldRt != nil {
		// wtf?
		logrus.WithField("container", c.Name).Error("runtime state already exists!")
		oldRt.Close()
	}

	go func() {
		err := rt.agent.SyntheticWaitForClose()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to wait for agent to close")
		}

		// agent stopped.
		// if this is unexpected (i.e. container is still running and has a matching runtime state -- so same run), then stop it
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.runtimeState.Load() == rt {
			logrus.WithField("container", c.Name).Error("agent stopped unexpectedly, stopping container")
			_, err := c.stopLocked(StopOptions{})
			if err != nil {
				logrus.WithError(err).WithField("container", c.Name).Error("failed to stop container")
			}
		}
	}()

	return rt, nil
}

func (rt *ContainerRuntimeState) UseAgent(f func(a *agent.Client) error) error {
	if rt.freezer != nil {
		rt.freezer.IncRef()
		defer rt.freezer.DecRef()
	}
	return f(rt.agent)
}

func (rt *ContainerRuntimeState) Close() error {
	rt.listenersDebounce.Cancel()
	if rt.agent != nil {
		rt.agent.Close()
	}
	if rt.freezer != nil {
		rt.freezer.Close()
	}
	if rt.bpf != nil {
		rt.bpf.Close()
	}

	// mandatory to create a runtime state
	rt.InitPidfd.Close()
	rt.InitProcDirfd.Close()
	return nil
}
