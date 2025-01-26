package bpf

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"golang.org/x/sys/unix"
)

//go:generate ./build-bpf.sh

type ContainerBpfManager struct {
	mu syncx.Mutex

	cgPath      string
	netnsCookie uint64

	closers []io.Closer

	lfwdBlockedPorts *ebpf.Map
	// refcount ports to block
	// keep a port blocked if ANY listeners, v4 OR v6, are using it
	// protected by ctr.mu
	lfwdBlockedPortRefs map[uint16]int
}

func NewContainerBpfManager(cgPath string, netnsCookie uint64) (*ContainerBpfManager, error) {
	return &ContainerBpfManager{
		cgPath:      cgPath,
		netnsCookie: netnsCookie,

		lfwdBlockedPortRefs: make(map[uint16]int),
	}, nil
}

func (b *ContainerBpfManager) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var errs []error
	for _, c := range b.closers {
		err := c.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *ContainerBpfManager) LfwdBlockPort(port uint16) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.lfwdBlockedPorts == nil {
		return nil
	}

	// refcount
	b.lfwdBlockedPortRefs[port]++
	// first ref?
	if b.lfwdBlockedPortRefs[port] == 1 {
		// swap to big endian
		port = (port&0xff)<<8 | (port&0xff00)>>8
		return b.lfwdBlockedPorts.Put(port, byte(1))
	}

	return nil
}

func (b *ContainerBpfManager) LfwdUnblockPort(port uint16) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.lfwdBlockedPorts == nil {
		return nil
	}

	// refcount
	b.lfwdBlockedPortRefs[port]--
	// last ref?
	if b.lfwdBlockedPortRefs[port] == 0 {
		// swap to big endian
		port = (port&0xff)<<8 | (port&0xff00)>>8
		return b.lfwdBlockedPorts.Delete(port)
	}

	return nil
}

func (b *ContainerBpfManager) attachOneCgLocked(typ ebpf.AttachType, prog *ebpf.Program) error {
	l, err := link.AttachCgroup(link.CgroupOptions{
		Path:    b.cgPath,
		Attach:  typ,
		Program: prog,
	})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	b.closers = append(b.closers, l)
	return nil
}

func (b *ContainerBpfManager) AttachLfwd() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// must load a new instance to set a different netns cookie in config map
	// maps are per-program instance
	// and this is an unpinned program (no ref in /sys/fs/bpf), so it'll be destroyed
	// when we close fds
	spec, err := loadLfwd()
	if err != nil {
		return fmt.Errorf("load spec: %w", err)
	}

	// set netns cookie filter
	err = spec.RewriteConstants(map[string]any{
		"config_netns_cookie": b.netnsCookie,
	})
	if err != nil {
		return fmt.Errorf("configure: %w", err)
	}

	objs := &lfwdObjects{}
	err = spec.LoadAndAssign(objs, nil)
	if err != nil {
		return fmt.Errorf("load objs: %w", err)
	}
	b.closers = append(b.closers, objs)

	err = b.attachOneCgLocked(ebpf.AttachCGroupInet4Connect, objs.LfwdConnect4)
	if err != nil {
		return err
	}

	// lfwd
	err = b.attachOneCgLocked(ebpf.AttachCGroupUDP4Sendmsg, objs.LfwdSendmsg4)
	if err != nil {
		return err
	}

	err = b.attachOneCgLocked(ebpf.AttachCgroupInet4GetPeername, objs.LfwdGetpeername4)
	if err != nil {
		return err
	}
	err = b.attachOneCgLocked(ebpf.AttachCGroupInet6Connect, objs.LfwdConnect6)
	if err != nil {
		return err
	}
	err = b.attachOneCgLocked(ebpf.AttachCGroupUDP6Sendmsg, objs.LfwdSendmsg6)
	if err != nil {
		return err
	}
	err = b.attachOneCgLocked(ebpf.AttachCgroupInet6GetPeername, objs.LfwdGetpeername6)
	if err != nil {
		return err
	}

	b.lfwdBlockedPorts = objs.lfwdMaps.BlockedPorts
	return nil
}

func checkIsNsfs(entry fs.DirEntry) bool {
	// check if it's a namespace. docker leaves non-bind-mounted files behind until GC
	fd, err := unix.Open("/run/docker/netns/"+entry.Name(), unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)

	_, err = unix.IoctlGetInt(fd, unix.NS_GET_NSTYPE)
	return err == nil
}
