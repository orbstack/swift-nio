package main

import (
	"errors"

	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

type NfsMirror interface {
	Mount(source string, subdest string, fstype string, flags uintptr, data string) error
	MountBind(source string, subdest string) error
	Unmount(subdest string) error
	Close() error
	MountImage(img *dockertypes.FullImage) error
}

type MultiNfsMirror struct {
	mirrors []NfsMirror
}

func NewMultiNfsMirror(mirrors ...NfsMirror) *MultiNfsMirror {
	return &MultiNfsMirror{
		mirrors: mirrors,
	}
}

func (m *MultiNfsMirror) Mount(source string, subdest string, fstype string, flags uintptr, data string) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.Mount(source, subdest, fstype, flags, data)
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

func (m *MultiNfsMirror) MountImage(img *dockertypes.FullImage) error {
	var errs []error
	for _, mirror := range m.mirrors {
		err := mirror.MountImage(img)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
