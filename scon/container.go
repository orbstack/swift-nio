package main

import (
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type ContainerState int

const (
	ContainerStateStopped ContainerState = iota
	ContainerStateRunning
)

type ContainerHooks interface {
	Config(*Container, func(string, string)) (string, error)
	PreStart(*Container) error
	PostStart(*Container) error
}

type Container struct {
	ID        string
	Name      string
	Image     types.ImageSpec
	dir       string
	rootfsDir string

	builtin bool
	// state
	state    ContainerState
	creating bool
	deleting bool

	hooks ContainerHooks

	lxc       *lxc.Container
	lxcParams LxcForkParams

	agent   syncx.CondValue[*agent.Client]
	manager *ConManager
	mu      sync.RWMutex

	seccompCookie     uint64
	lastListeners     []agent.ProcListener
	autofwdDebounce   syncx.FuncDebounce
	lastAutofwdUpdate time.Time
}

func (m *ConManager) newContainerLocked(record *types.ContainerRecord) (*Container, error) {
	id := record.ID
	dir := m.subdir("containers", id)

	c := &Container{
		ID:      record.ID,
		Name:    record.Name,
		Image:   record.Image,
		builtin: record.Builtin,
		state:   ContainerStateStopped,
		dir:     dir,
		manager: m,
		agent:   syncx.NewCondValue[*agent.Client](nil, nil),
	}

	// special-case hooks for docker
	if c.builtin && c.Image.Distro == images.ImageDocker {
		c.hooks = &DockerHooks{}
	}

	// create lxc
	// fills in c and seccomp cookie
	err := c.initLxc()
	if err != nil {
		return nil, err
	}

	// ensure rootfs exists. we'll need it eventually: nfs, create, and start.
	err = os.MkdirAll(c.rootfsDir, 0755)
	if err != nil {
		return nil, err
	}

	c.autofwdDebounce = syncx.NewFuncDebounce(autoForwardDebounce, func() {
		err := c.updateListenersDirect()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to update listeners")
		}
	})
	m.seccompCookies[c.seccompCookie] = c

	return c, nil
}

func (c *Container) Agent() *agent.Client {
	return c.agent.Wait()
}

func (c *Container) Exec(cmd []string, opts lxc.AttachOptions, extraFd int) (int, error) {
	// no new fds in between
	syscall.ForkLock.Lock()
	defer syscall.ForkLock.Unlock()

	// TODO cloexec safety
	// critical section
	if extraFd != 0 {
		// clear cloexec
		unix.FcntlInt(uintptr(extraFd), unix.F_SETFD, 0)
		defer unix.CloseOnExec(extraFd)
	}
	return c.lxc.RunCommandNoWait(cmd, opts)
}

func (c *Container) Running() bool {
	return c.lxc.Running()
}

func (c *Container) toRecord() *types.ContainerRecord {
	return &types.ContainerRecord{
		ID:    c.ID,
		Name:  c.Name,
		Image: c.Image,

		Builtin:  c.builtin,
		Running:  c.Running(),
		Deleting: c.deleting,
	}
}

func (c *Container) persist() error {
	if c.builtin {
		return nil
	}

	record := c.toRecord()
	logrus.WithField("record", record).Debug("persisting container")
	return c.manager.db.SetContainer(c.ID, record)
}

func (c *Container) refreshState() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	logrus.WithField("container", c.Name).Debug("refreshing container state")
	stateRunning := c.state == ContainerStateRunning
	lxcRunning := c.Running()
	if lxcRunning != stateRunning {
		if lxcRunning {
			err := c.onStart()
			if err != nil {
				return err
			}
		} else {
			err := c.onStop()
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Container) addDeviceNodeLocked(src string, dst string) error {
	err := c.lxc.AddDeviceNode(src, dst)
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) addDeviceNode(src string, dst string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.addDeviceNodeLocked(src, dst)
}

func (c *Container) removeDeviceNode(src string, dst string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	err := c.lxc.RemoveDeviceNode(src, dst)
	if err != nil {
		return err
	}

	return nil
}
