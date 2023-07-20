package bpf

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/sirupsen/logrus"
)

//go:generate ./build-bpf.sh

type BpfManager struct {
	closers          []io.Closer
	lfwdBlockedPorts *ebpf.Map
	ptrackNotify     *ringbuf.Reader
}

func NewBpfManager() (*BpfManager, error) {
	return &BpfManager{}, nil
}

func (b *BpfManager) Close() error {
	var errs []error
	for _, c := range b.closers {
		err := c.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (b *BpfManager) LfwdBlockPort(port uint16) error {
	if b.lfwdBlockedPorts == nil {
		return nil
	}

	// swap to big endian
	port = (port&0xff)<<8 | (port&0xff00)>>8
	return b.lfwdBlockedPorts.Put(port, byte(1))
}

func (b *BpfManager) LfwdUnblockPort(port uint16) error {
	if b.lfwdBlockedPorts == nil {
		return nil
	}

	// swap to big endian
	port = (port&0xff)<<8 | (port&0xff00)>>8
	return b.lfwdBlockedPorts.Delete(port)
}

func (b *BpfManager) AttachLfwd(cgPath string, netnsCookie uint64) error {
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
		"config_netns_cookie": netnsCookie,
	})
	if err != nil {
		return fmt.Errorf("configure: %w", err)
	}

	objs := lfwdObjects{}
	err = spec.LoadAndAssign(&objs, nil)
	if err != nil {
		return fmt.Errorf("load objs: %w", err)
	}
	b.closers = append(b.closers, &objs)

	attachOne := func(typ ebpf.AttachType, prog *ebpf.Program) error {
		l, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgPath,
			Attach:  typ,
			Program: prog,
		})
		if err != nil {
			return fmt.Errorf("attach: %w", err)
		}
		b.closers = append(b.closers, l)
		return nil
	}

	err = attachOne(ebpf.AttachCGroupInet4Connect, objs.LfwdConnect4)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCGroupUDP4Sendmsg, objs.LfwdSendmsg4)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCgroupInet4GetPeername, objs.LfwdGetpeername4)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCGroupInet6Connect, objs.LfwdConnect6)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCGroupUDP6Sendmsg, objs.LfwdSendmsg6)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCgroupInet6GetPeername, objs.LfwdGetpeername6)
	if err != nil {
		return err
	}

	b.lfwdBlockedPorts = objs.lfwdMaps.BlockedPorts
	return nil
}

func (b *BpfManager) AttachPtrack(cgPath string, netnsCookie uint64) error {
	// must load a new instance to set a different netns cookie in config map
	// maps are per-program instance
	// and this is an unpinned program (no ref in /sys/fs/bpf), so it'll be destroyed
	// when we close fds
	spec, err := loadPtrack()
	if err != nil {
		return fmt.Errorf("load spec: %w", err)
	}

	// set netns cookie filter
	err = spec.RewriteConstants(map[string]any{
		"config_netns_cookie": netnsCookie,
	})
	if err != nil {
		return fmt.Errorf("configure: %w", err)
	}

	objs := ptrackObjects{}
	err = spec.LoadAndAssign(&objs, nil)
	if err != nil {
		return fmt.Errorf("load objs: %w", err)
	}
	b.closers = append(b.closers, &objs)

	attachOne := func(typ ebpf.AttachType, prog *ebpf.Program) error {
		l, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgPath,
			Attach:  typ,
			Program: prog,
		})
		if err != nil {
			return fmt.Errorf("attach: %w", err)
		}
		b.closers = append(b.closers, l)
		return nil
	}

	err = attachOne(ebpf.AttachCGroupInet4Bind, objs.PtrackBind4)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCGroupInet6Bind, objs.PtrackBind6)
	if err != nil {
		return err
	}

	err = attachOne(ebpf.AttachCgroupInetSockRelease, objs.PtrackSockRelease)
	if err != nil {
		return err
	}

	reader, err := ringbuf.NewReader(objs.ptrackMaps.NotifyRing)
	if err != nil {
		return fmt.Errorf("create reader: %w", err)
	}
	b.closers = append(b.closers, reader)
	b.ptrackNotify = reader

	return nil
}

func (b *BpfManager) MonitorPtrack(fn func() error) error {
	var rec ringbuf.Record
	for {
		// read one event
		err := b.ptrackNotify.ReadInto(&rec)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return nil
			} else {
				return fmt.Errorf("read: %w", err)
			}
		}

		// trigger callback
		err = fn()
		if err != nil {
			logrus.WithError(err).Error("ptrack callback failed")
		}
	}
}
