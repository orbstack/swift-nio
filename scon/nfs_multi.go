package main

import (
	"errors"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

type NfsMirror interface {
	Mount(source string, subdest string, fstype string, flags uintptr, data string, mountFd int) error
	MountBind(source string, subdest string) error
	Unmount(subdest string) error
	Close() error
	MountImage(img *dockertypes.FullImage, tag string, fs *securefs.FS) error
	UnmountAll(prefix string) error
}

type MultiNfsMirror struct {
	mirrors []NfsMirror
}

func NewMultiNfsMirror(mirrors ...NfsMirror) *MultiNfsMirror {
	return &MultiNfsMirror{
		mirrors: mirrors,
	}
}

func (m *MultiNfsMirror) Mount(source string, subdest string, fstype string, flags uintptr, data string, mountFd int) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.Mount(source, subdest, fstype, flags, data, mountFd)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *MultiNfsMirror) MountBind(source string, subdest string) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.MountBind(source, subdest)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *MultiNfsMirror) Unmount(subdest string) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.Unmount(subdest)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *MultiNfsMirror) Close() error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.Close()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *MultiNfsMirror) MountImage(img *dockertypes.FullImage, tag string, fs *securefs.FS) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.MountImage(img, tag, fs)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *MultiNfsMirror) UnmountAll(prefix string) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.UnmountAll(prefix)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
