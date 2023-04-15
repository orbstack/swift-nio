package main

import (
	"os"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/scon/isclient/istypes"
)

type fsnotifyInjector struct {
}

func writeOneEvent(path string, typ string) error {
	return os.WriteFile("/proc/.orbinternal/"+typ, []byte(mounts.Virtiofs+path), 0644)
}

func newFsnotifyInjector() *fsnotifyInjector {
	return &fsnotifyInjector{}
}

func (f *fsnotifyInjector) Inject(events istypes.FsnotifyEventsBatch) error {
	//TODO: batched ioctl, pack all into a string
	for i, path := range events.Paths {
		flags := events.Flags[i]

		// order: create, modify, attrib, unlink
		if flags&istypes.FsnotifyEventCreate != 0 {
			err := writeOneEvent(path, "create")
			if err != nil {
				return err
			}
		}

		if flags&istypes.FsnotifyEventModify != 0 {
			err := writeOneEvent(path, "modify")
			if err != nil {
				return err
			}
		}

		if flags&istypes.FsnotifyEventStatAttr != 0 {
			err := writeOneEvent(path, "attrib")
			if err != nil {
				return err
			}
		}

		if flags&istypes.FsnotifyEventRemove != 0 {
			err := writeOneEvent(path, "unlink")
			if err != nil {
				return err
			}
		}
	}

	return nil
}
