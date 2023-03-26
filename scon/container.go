package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/images"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/kdrag0n/macvirt/scon/types"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/kdrag0n/macvirt/scon/util/sysnet"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	ErrAgentDead  = errors.New("agent dead or not responding")
	ErrNotRunning = errors.New("container is not running")
)

const (
	agentTimeout = 15 * time.Second
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
	state types.ContainerState

	hooks ContainerHooks

	lxc       *lxc.Container
	lxcParams LxcForkParams

	agent   syncx.CondValue[*agent.Client]
	manager *ConManager
	mu      syncx.RWMutex

	seccompCookie     uint64
	lastListeners     []sysnet.ProcListener
	autofwdDebounce   syncx.FuncDebounce
	lastAutofwdUpdate time.Time
	inetDiagFile      *os.File

	// docker
	freezer *Freezer
}

func (m *ConManager) newContainerLocked(record *types.ContainerRecord) (*Container, error) {
	id := record.ID
	dir := m.subdir("containers", id)

	c := &Container{
		ID:      record.ID,
		Name:    record.Name,
		Image:   record.Image,
		builtin: record.Builtin,
		// always create in stopped state
		state:   types.ContainerStateStopped,
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

		Builtin: c.builtin,
		State:   c.state,
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
	stateRunning := c.state == types.ContainerStateRunning
	lxcRunning := c.Running()
	if lxcRunning != stateRunning {
		if lxcRunning {
			err := c.onStartLocked()
			if err != nil {
				return err
			}
		} else {
			err := c.onStopLocked()
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

	// can't use lxc.RemoveDeviceNode because node is already gone from host
	// just delete the node in the container
	// don't bother to update the devices cgroup bpf filter
	err := c.UseAgent(func(a *agent.Client) error {
		return a.RemoveFile(dst)
	})
	if err != nil {
		return err
	}

	return nil
}

// can't use rlock - could be called with container lock held (updateListeners)
func (c *Container) useAgentInternal(fn func(*agent.Client) error, takeFreezerRef bool) error {
	if !c.Running() {
		return ErrNotRunning
	}

	// we want it to be unfrozen - or call will hang
	freezer := c.Freezer()
	if takeFreezerRef && freezer != nil {
		freezer.IncRef()
		defer freezer.DecRef()
	}

	agent, err := util.WithTimeout(func() (*agent.Client, error) {
		return c.agent.Wait(), nil
	}, agentTimeout)
	if err != nil {
		return ErrAgentDead
	}

	return fn(agent)
}

func (c *Container) UseAgent(fn func(*agent.Client) error) error {
	return c.useAgentInternal(fn, true)
}

func UseAgentRet[T any](c *Container, fn func(*agent.Client) (T, error)) (T, error) {
	var ret T
	err := c.UseAgent(func(agent *agent.Client) error {
		var err error
		ret, err = fn(agent)
		return err
	})
	return ret, err
}

func (c *Container) Freezer() *Freezer {
	return c.freezer
}

func (c *Container) setStateInternalLocked(state types.ContainerState, isInternal bool) (types.ContainerState, error) {
	if c.state == state {
		return "", nil
	}

	if !c.state.CanTransitionTo(state, isInternal) {
		return "", fmt.Errorf("invalid state transition from %v to %v", c.state, state)
	}

	// non-internal transitions are not allowed while manager is stopping
	// otherwise we may persist wrong state
	if !isInternal && c.manager.stopping {
		return "", fmt.Errorf("invalid state transition from %v to %v while manager is stopping", c.state, state)
	}

	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"from":      c.state,
		"to":        state,
	}).Debug("transitioning container state")

	oldState := c.state
	c.state = state

	err := c.persist()
	if err != nil {
		return "", err
	}

	return oldState, nil
}

func (c *Container) setStateLocked(state types.ContainerState) (types.ContainerState, error) {
	return c.setStateInternalLocked(state, false)
}

func (c *Container) revertStateLocked(oldState types.ContainerState) {
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"from":      c.state,
		"to":        oldState,
	}).Debug("reverting container state")

	c.state = oldState
}
