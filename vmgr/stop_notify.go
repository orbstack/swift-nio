package vmgr

import (
	"github.com/fsnotify/fsnotify"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/orbstack/macvirt/vmgr/util/pspawn"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func doUninstallCleanup() error {
	logrus.Info("uninstall - cleaning up")

	// revert docker context
	err := pspawn.Command(conf.FindXbin("docker"), "context", "use", "default").Run()
	if err != nil {
		return err
	}

	return err
}

func WatchCriticalFiles(stopCh chan<- types.StopRequest) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	err = watcher.AddWithEvents(conf.DataImage(), unix.NOTE_DELETE)
	if err != nil {
		return err
	}

	for {
		select {
		case event := <-watcher.Events:
			logrus.Debugf("Critical file event: %#v", event)
			if event.Op&fsnotify.Remove == fsnotify.Remove {
				logrus.Info("Data image deleted, stopping VM")

				// clean up for uninstall
				err := doUninstallCleanup()
				if err != nil {
					logrus.WithError(err).Error("uninstall cleanup failed")
				}

				// force is ok - data doesn't matter anymore
				stopCh <- types.StopRequest{Type: types.StopTypeForce, Reason: types.StopReasonUninstall}
				return nil
			}
		case err := <-watcher.Errors:
			return err
		}
	}
}
