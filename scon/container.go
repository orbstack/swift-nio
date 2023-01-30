package main

import (
	"sync"
	"syscall"
	"time"

	"github.com/kdrag0n/macvirt/scon/agent"
	"github.com/kdrag0n/macvirt/scon/syncx"
	"github.com/lxc/go-lxc"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type ContainerHooks interface {
	Config(*Container, func(string, string)) (string, error)
	PreStart(*Container) error
}

type Container struct {
	ID        string
	Name      string
	Image     ImageSpec
	dir       string
	rootfsDir string

	builtin bool
	// state
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

func (c *Container) persist() error {
	if c.builtin {
		return nil
	}

	record := &ContainerRecord{
		ID:    c.ID,
		Name:  c.Name,
		Image: c.Image,

		Running:  c.Running(),
		Deleting: c.deleting,
	}
	logrus.WithField("record", record).Debug("persisting container")
	return c.manager.db.SetContainer(c.ID, record)
}
