package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/lxc/go-lxc"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/cuser"
	"github.com/orbstack/macvirt/scon/util/securefs"
	"github.com/orbstack/macvirt/scon/util/sysnet"
	"github.com/orbstack/macvirt/scon/util/sysns"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var noopHooks = &NoopHooks{}

var (
	ErrAgentDead         = errors.New("agent not running")
	ErrMachineNotRunning = errors.New("machine not running")
	ErrNoMachines        = errors.New("no machines found")
)

type containerConfigMethods struct {
	set  func(string, string)
	bind func(string, string, string)
}

type Container struct {
	// read-only
	manager    *ConManager
	mu         syncx.RWMutex
	hooks      ContainerHooks
	jobManager util.EntityJobManager
	holds      util.MutationHoldManager

	// read-only info
	ID      string
	Image   types.ImageSpec
	builtin bool

	// mutable
	Name   string
	config types.MachineConfig

	dataDir   string
	rootfsDir string
	quotaDir  string

	// LXC
	lxc           *lxc.Container
	lxcConfigured bool

	// state
	state          atomic.Pointer[types.ContainerState]
	isProvisioning atomic.Bool

	// if running
	runtimeState atomic.Pointer[ContainerRuntimeState]
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
		dataDir:   dir,
		quotaDir:  dir,
		manager:   m,
		rootfsDir: dir + "/rootfs",
		hooks:     noopHooks,

		jobManager: *util.NewEntityJobManager(m.ctx),
		holds:      *util.NewMutationHoldManager(),
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
		c.rootfsDir = conf.C().DockerRootfs
		c.quotaDir = conf.C().DockerDataDir
	}

	// create lxc
	err := c.initLxc()
	if err != nil {
		return nil, err
	}

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

func (c *Container) RealState() types.ContainerState {
	return *c.state.Load()
}

// state reported to DB and GUI; reports 'provisioning' when container is
// created but not fully initialized. ensures that container isn't accessible
// in GUI while still initializing, and that 'provisioning' remains reported in the DB
// so the container is cleaned up if scon is stopped while the machine is being set up.
func (c *Container) PersistedState() types.ContainerState {
	state := *c.state.Load()
	// also report 'provisioning' if it's starting, to prevent scon being stopped while container is starting
	// after being created -> doesn't get cleaned up afterwards
	if c.isProvisioning.Load() && (state == types.ContainerStateRunning || state == types.ContainerStateStarting) {
		return types.ContainerStateProvisioning
	} else {
		return state
	}
}

func (c *Container) setState(state types.ContainerState) {
	c.state.Store(&state)
}

func (c *Container) Running() bool {
	// currently the same
	return c.runningLocked()
}

func (c *Container) runningLocked() bool {
	return c.RealState() == types.ContainerStateRunning
}

func (c *Container) toRecord() *types.ContainerRecord {
	return &types.ContainerRecord{
		ID:    c.ID,
		Name:  c.Name,
		Image: c.Image,

		Config: c.config,

		Builtin: c.builtin,
		State:   c.PersistedState(),
	}
}

func (c *Container) getInfo() (*types.ContainerInfo, error) {
	var sizePtr *uint64

	// if quota dir exists, get size
	if err := unix.Access(c.quotaDir, unix.F_OK); err != unix.ENOENT {
		diskSize, err := c.manager.fsOps.GetSubvolumeSize(c.quotaDir)
		if err != nil {
			logrus.WithError(err).WithField("container", c.Name).Error("failed to get subvolume size")
		} else {
			sizePtr = diskSize
		}
	}

	return &types.ContainerInfo{
		Record:   c.toRecord(),
		DiskSize: sizePtr,
	}, nil
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
	if c.runningLocked() && !c.lxc.Running() {
		err := c.onStopLocked()
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *Container) addDeviceNode(src string, dst string) error {
	err := c.lxc.AddDeviceNode(src, dst)
	if err != nil {
		// lxc doesn't use %w
		if strings.HasPrefix(err.Error(), "container is not running:") {
			return ErrMachineNotRunning
		} else {
			return err
		}
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

func (c *Container) UseAgent(fn func(*agent.Client) error) error {
	rt, err := c.RuntimeState()
	if err != nil {
		return err
	}

	return rt.UseAgent(fn)
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
	rt, err := c.RuntimeState()
	if err != nil {
		return zero, err
	}

	return sysnet.WithNetnsFile(rt.InitPidfd, fn)
}

func withContainerMountNs[T any](c *Container, fn func() (T, error)) (T, error) {
	var zero T
	rt, err := c.RuntimeState()
	if err != nil {
		return zero, err
	}
	defer runtime.KeepAlive(rt.InitPidfd)

	// .Fd() ok: dirfds are not pollable
	return sysns.WithMountNs(int(rt.InitPidfd.Fd()), fn)
}

func (c *Container) UseMountNs(fn func() error) error {
	_, err := withContainerMountNs(c, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

func (c *Container) transitionStateInternalLocked(newState types.ContainerState, isInternal bool) (types.ContainerState, error) {
	oldState := c.RealState()
	if c.RealState() == newState {
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
		"from":      c.RealState(),
		"to":        oldState,
	}).Debug("reverting container state")

	c.setState(oldState)
}

func (c *Container) getDefaultUidGid() (int, int, error) {
	rootfs, err := securefs.NewFromPath(c.rootfsDir)
	if err != nil {
		return -1, -1, fmt.Errorf("open rootfs: %w", err)
	}
	defer rootfs.Close()

	guestUser, err := cuser.LookupUser(c.config.DefaultUsername, rootfs)
	if err != nil {
		return -1, -1, fmt.Errorf("lookup user: %w", err)
	}

	uid, err := strconv.ParseUint(guestUser.Uid, 10, 32)
	if err != nil {
		return -1, -1, fmt.Errorf("parse uid: %w", err)
	}

	gid, err := strconv.ParseUint(guestUser.Gid, 10, 32)
	if err != nil {
		return -1, -1, fmt.Errorf("parse gid: %w", err)
	}

	return int(uid), int(gid), nil
}
