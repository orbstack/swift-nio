package main

import (
	"errors"

	"github.com/orbstack/macvirt/scon/util/securefs"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

type NfsMirror interface {
	Mount(source string, subdest string, fstype string, flags uintptr, data string, clientUid int, clientGid int, mountFunc func(destPath string) error) error
	MountBind(source string, subdest string, clientUid int, clientGid int) error
	Unmount(subdest string) error
	Flush() error
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

func (m *MultiNfsMirror) Mount(source string, subdest string, fstype string, flags uintptr, data string, clientUid int, clientGid int, mountFunc func(destPath string) error) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.Mount(source, subdest, fstype, flags, data, clientUid, clientGid, mountFunc)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *MultiNfsMirror) MountBind(source string, subdest string, clientUid int, clientGid int) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.MountBind(source, subdest, clientUid, clientGid)
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

func (m *MultiNfsMirror) Flush() error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.Flush()
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
