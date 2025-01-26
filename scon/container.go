package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lxc/go-lxc"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/cuser"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/scon/util/sysns"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var (
	ErrAgentDead         = errors.New("agent not running")
	ErrMachineNotRunning = errors.New("machine not running")
	ErrNoMachines        = errors.New("no machines found")
)

type containerConfigMethods struct {
	set  func(string, string)
	bind func(string, string, string)
}

type ContainerHooks interface {
	Config(*Container, containerConfigMethods) (string, error)
	PreStart(*Container) error
	PostStart(*Container) error
	PostStop(*Container) error
}

type Container struct {
	ID        string
	Name      string
	Aliases   []string
	Image     types.ImageSpec
	dir       string
	rootfsDir string

	builtin bool
	config  types.MachineConfig
	// state
	state atomic.Pointer[types.ContainerState]

	hooks ContainerHooks

	lxc           *lxc.Container
	lxcConfigured bool

	manager *ConManager
	mu      syncx.RWMutex

	jobManager *util.EntityJobManager
	holds      *util.MutationHoldManager

	// if booted
	// TODO: move all this into a .rt field (RuntimeState)
	lastListeners     []sysnet.ListenerInfo
	autofwdDebounce   syncx.FuncDebounce
	lastAutofwdUpdate time.Time
	fwdDirtyFlags     uint32
	agent             atomic.Pointer[agent.Client]
	bpf               *bpf.ContainerBpfManager
	ipAddrsMu         syncx.Mutex
	ipAddrs           []net.IP
	initPid           int
	initPidFile       *os.File

	// docker
	freezer atomic.Pointer[Freezer]
}

func (m *ConManager) newContainerLocked(record *types.ContainerRecord) (*Container, error) {
	id := record.ID
	// m.subdir calls MkdirAll
	dir := m.dataDir + "/containers/" + id

	c := &Container{
		ID:        record.ID,
		Name:      record.Name,
		Image:     record.Image,
		builtin:   record.Builtin,
		config:    record.Config,
		dir:       dir,
		manager:   m,
		rootfsDir: dir + "/rootfs",

		jobManager: util.NewEntityJobManager(m.ctx),
		holds:      util.NewMutationHoldManager(),
	}
	// always create in stopped state
	c.setState(types.ContainerStateStopped)

	// special-case hooks for docker
	if c.builtin && c.Image.Distro == images.ImageDocker {
		hooks, err := newDockerHooks(m)
		if err != nil {
			return nil, err
		}
		c.hooks = hooks
	}

	// create parent as subvolume first
	err := c.manager.fsOps.CreateSubvolumeIfNotExists(c.dir)
	if err != nil {
		return nil, fmt.Errorf("create subvolume: %w", err)
	}

	// create lxc
	// this also creates rootfs dir
	err = c.initLxc()
	if err != nil {
		return nil, err
	}

	c.autofwdDebounce = syncx.NewFuncDebounce(autoForwardDebounce, func() {
		// TODO: use flags to reduce update work
		atomic.SwapUint32(&c.fwdDirtyFlags, 0)
		err := c.updateListenersNow()
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to update listeners")
		}
	})

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
		_, err := unix.FcntlInt(uintptr(extraFd), unix.F_SETFD, 0)
		if err != nil {
			return 0, err
		}
		defer unix.CloseOnExec(extraFd)
	}
	return c.lxc.RunCommandNoWait(cmd, opts)
}

func (c *Container) State() types.ContainerState {
	return *c.state.Load()
}

func (c *Container) setState(state types.ContainerState) {
	c.state.Store(&state)
}

func (c *Container) Running() bool {
	// currently the same
	return c.runningLocked()
}

func (c *Container) runningLocked() bool {
	return c.State() == types.ContainerStateRunning
}

func (c *Container) lxcRunning() bool {
	return c.lxc.Running()
}

func (c *Container) toRecord() *types.ContainerRecord {
	return &types.ContainerRecord{
		ID:    c.ID,
		Name:  c.Name,
		Image: c.Image,

		Config: c.config,

		Builtin: c.builtin,
		State:   c.State(),
	}
}

func (c *Container) persist() error {
	// we do still persist builtin containers, we just ignore most fields when reading
	record := c.toRecord()
	logrus.WithField("record", record).Debug("persisting container")

	err := c.manager.db.SetContainer(c.ID, record)
	if err != nil {
		return err
	}

	// also notify UI of state change
	c.manager.uiEventDebounce.Call()

	return nil
}

func (c *Container) refreshState() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	logrus.WithField("container", c.Name).Debug("refreshing container state")
	stateRunning := c.runningLocked()
	lxcRunning := c.lxcRunning()
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

func (c *Container) addDeviceNode(src string, dst string) error {
	err := c.lxc.AddDeviceNode(src, dst)
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) removeDeviceNode(dst string) error {
	// can't use lxc.RemoveDeviceNode because node is already gone from host
	// just delete the node in the container
	// don't bother to update the devices cgroup bpf filter
	err := c.UseMountNs(func() error {
		return os.Remove(dst)
	})
	if err != nil {
		return err
	}

	return nil
}

func (c *Container) acquireAgent(needFreezerRef bool, needLock bool) (*Freezer, *agent.Client, error) {
	// only keep lock for duration of agent acquire
	if needLock {
		c.mu.RLock()
		defer c.mu.RUnlock()
	}

	if !c.Running() {
		return nil, nil, ErrMachineNotRunning
	}

	// we want it to be unfrozen - or call will hang
	var freezer *Freezer
	if needFreezerRef {
		freezer = c.Freezer()
		if freezer != nil {
			freezer.IncRef()
		}
	}

	agent := c.agent.Load()
	if agent == nil {
		if freezer != nil {
			freezer.DecRef()
		}
		return nil, nil, ErrAgentDead
	}

	return freezer, agent, nil
}

// must be called with lock held in case container is in the middle of starting,
// freezer is not yet created but agent is
func (c *Container) useAgentInternal(fn func(*agent.Client) error, needFreezerRef bool, needLock bool) error {
	freezer, agent, err := c.acquireAgent(needFreezerRef, needLock)
	if err != nil {
		return err
	}

	if freezer != nil {
		defer freezer.DecRef()
	}

	// ... so we make the actual agent call outside the lock (if caller isn't locked)
	return fn(agent)
}

func (c *Container) useAgentLocked(fn func(*agent.Client) error) error {
	return c.useAgentInternal(fn /*needFreezerRef*/, true /*needLock*/, false)
}

func (c *Container) UseAgent(fn func(*agent.Client) error) error {
	return c.useAgentInternal(fn /*needFreezerRef*/, true /*needLock*/, true)
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

func UseAgentRet2[T, U any](c *Container, fn func(*agent.Client) (T, U, error)) (T, U, error) {
	var ret1 T
	var ret2 U
	err := c.UseAgent(func(agent *agent.Client) error {
		var err error
		ret1, ret2, err = fn(agent)
		return err
	})
	return ret1, ret2, err
}

func withContainerNetns[T any](c *Container, fn func() (T, error)) (T, error) {
	var zero T
	initPidF := c.initPidFile
	if initPidF == nil {
		return zero, fmt.Errorf("no init pid")
	}

	return sysnet.WithNetns(initPidF, fn)
}

func withContainerMountNs[T any](c *Container, fn func() (T, error)) (T, error) {
	var zero T
	initPidF := c.initPidFile
	if initPidF == nil {
		return zero, fmt.Errorf("no init pid")
	}
	defer runtime.KeepAlive(initPidF)

	return util.UseFile1(initPidF, func(fd int) (T, error) {
		return sysns.WithMountNs(fd, fn)
	})
}

func (c *Container) UseMountNs(fn func() error) error {
	_, err := withContainerMountNs(c, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

func (c *Container) Freezer() *Freezer {
	return c.freezer.Load()
}

func (c *Container) transitionStateInternalLocked(newState types.ContainerState, isInternal bool) (types.ContainerState, error) {
	oldState := c.State()
	if c.State() == newState {
		return "", nil
	}

	if !oldState.CanTransitionTo(newState, isInternal) {
		return "", fmt.Errorf("cannot transition from %v to %v", oldState, newState)
	}

	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"from":      oldState,
		"to":        newState,
	}).Debug("transitioning container state")

	c.setState(newState)

	// do not persist state transitions when manager is stopping
	if !c.manager.stopping.Load() {
		err := c.persist()
		if err != nil {
			return "", err
		}
	}

	return oldState, nil
}

func (c *Container) transitionStateLocked(state types.ContainerState) (types.ContainerState, error) {
	return c.transitionStateInternalLocked(state, false)
}

func (c *Container) revertStateLocked(oldState types.ContainerState) {
	logrus.WithFields(logrus.Fields{
		"container": c.Name,
		"from":      c.State(),
		"to":        oldState,
	}).Debug("reverting container state")

	c.setState(oldState)
}

func (c *Container) getDefaultUidGid() (int, int, error) {
	rootfs, err := securefs.NewFromPath(c.rootfsDir)
	if err != nil {
		return -1, -1, fmt.Errorf("open guest rootfs: %w", err)
	}
	defer rootfs.Close()

	guestUser, err := cuser.LookupUser(c.config.DefaultUsername, rootfs)
	if err != nil {
		return -1, -1, fmt.Errorf("lookup guest user: %w", err)
	}

	uid, err := strconv.Atoi(guestUser.Uid)
	if err != nil {
		return -1, -1, fmt.Errorf("parse guest uid: %w", err)
	}

	gid, err := strconv.Atoi(guestUser.Gid)
	if err != nil {
		return -1, -1, fmt.Errorf("parse guest gid: %w", err)
	}

	return uid, gid, nil
}
