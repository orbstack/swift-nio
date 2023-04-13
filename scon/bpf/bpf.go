package bpf

import (
	"errors"
	"fmt"
	"io"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

//go:generate ./build-bpf.sh

func AttachLfwd(cgPath string, netnsCookie uint64) (func() error, error) {
	var closers []io.Closer

	// must load a new instance to set a different netns cookie in config map
	// maps are per-program instance
	// and this is an unpinned program (no ref in /sys/fs/bpf), so it'll be destroyed
	// when we close fds
	objs := lfwdObjects{}
	err := loadLfwdObjects(&objs, nil)
	if err != nil {
		return nil, fmt.Errorf("load obj: %w", err)
	}
	closers = append(closers, &objs)

	// set netns cookie filter
	err = objs.ConfigMap.Update(uint32(0), netnsCookie, ebpf.UpdateAny)
	if err != nil {
		return nil, fmt.Errorf("update config map: %w", err)
	}

	attachOne := func(typ ebpf.AttachType, prog *ebpf.Program) error {
		l, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgPath,
			Attach:  typ,
			Program: prog,
		})
		if err != nil {
			return fmt.Errorf("attach: %w", err)
		}
		closers = append(closers, l)
		return nil
	}

	err = attachOne(ebpf.AttachCGroupInet4Connect, objs.LfwdConnect4)
	if err != nil {
		return nil, err
	}

	err = attachOne(ebpf.AttachCGroupUDP4Sendmsg, objs.LfwdSendmsg4)
	if err != nil {
		return nil, err
	}

	err = attachOne(ebpf.AttachCgroupInet4GetPeername, objs.LfwdGetpeername4)
	if err != nil {
		return nil, err
	}

	err = attachOne(ebpf.AttachCGroupInet6Connect, objs.LfwdConnect6)
	if err != nil {
		return nil, err
	}

	err = attachOne(ebpf.AttachCGroupUDP6Sendmsg, objs.LfwdSendmsg6)
	if err != nil {
		return nil, err
	}

	err = attachOne(ebpf.AttachCgroupInet6GetPeername, objs.LfwdGetpeername6)
	if err != nil {
		return nil, err
	}

	return func() error {
		var errs []error
		for _, c := range closers {
			err := c.Close()
			if err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}, nil
}
