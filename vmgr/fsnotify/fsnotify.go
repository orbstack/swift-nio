package fsnotify

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
)

// inactivity timeout for stopping dir change monitor
// must be much larger than ACTIVITY_NOTIFIER_INTERVAL_MS in libkrun
const dirChangeInactivityTimeout = 3 * time.Second

type VmNotifier struct {
	mu      sync.Mutex
	paths   []string
	swext   *vzf.FsVmNotifier
	network *vnet.Network
	stopCh  chan struct{}

	// mitigate virtiofs reentrancy risk by using a fine-grained lock
	dirChangeMu              sync.Mutex
	dirChangeStreamRef       unsafe.Pointer
	dirChangeRunning         atomic.Bool
	virtiofsActivityDebounce syncx.FuncDebounce
	monitorInactivity        bool
}

func NewVmNotifier(network *vnet.Network, monitorInactivity bool) (*VmNotifier, error) {
	swext, err := vzf.NewFsVmNotifier()
	if err != nil {
		return nil, fmt.Errorf("create swext: %w", err)
	}

	n := &VmNotifier{
		swext:             swext,
		network:           network,
		stopCh:            make(chan struct{}),
		monitorInactivity: monitorInactivity,
	}
	n.virtiofsActivityDebounce = syncx.NewFuncDebounce(dirChangeInactivityTimeout, n.onVirtiofsTimeout)
	return n, nil
}

func (n *VmNotifier) Run() error {
	n.swext.Start()

	nfsMountPath := coredir.NfsMountpoint()
	dataPath := conf.DataDir()

	n.dirChangeMu.Lock()
	var err error
	n.dirChangeStreamRef, err = vzf.SwextFseventsMonitorDirs(nfsMountPath, dataPath)
	n.dirChangeMu.Unlock()
	if err != nil {
		return fmt.Errorf("start dir monitor: %w", err)
	}

	conn, err := n.network.DialGuestTCPRetry(context.TODO(), ports.GuestKrpc)
	if err != nil {
		return fmt.Errorf("dial guest: %w", err)
	}
	defer conn.Close()

	client := NewKrpcClient(conn)

	// start debouncing activity?
	if n.monitorInactivity {
		n.virtiofsActivityDebounce.Call()
	}

	for {
		select {
		case buf := <-vzf.SwextFseventsKrpcEventsChan:
			err := client.WriteRaw(buf)
			if err != nil {
				logrus.WithError(err).Error("failed to send fsnotify events")
			}
		case <-n.stopCh:
			return nil
		}
	}
}

func (n *VmNotifier) Add(path string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	logrus.WithField("path", path).Debug("Adding fsnotify watch")

	n.paths = append(n.paths, path)
	err := n.swext.UpdatePaths(n.paths)
	if err != nil {
		return fmt.Errorf("update paths: %w", err)
	}

	return nil
}

func (n *VmNotifier) Remove(path string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	logrus.WithField("path", path).Debug("Removing fsnotify watch")

	for i, p := range n.paths {
		if p == path {
			n.paths = append(n.paths[:i], n.paths[i+1:]...)
			err := n.swext.UpdatePaths(n.paths)
			if err != nil {
				return fmt.Errorf("update paths: %w", err)
			}

			return nil
		}
	}

	return errors.New("path not tracked in notifier: " + path)
}

func (n *VmNotifier) ResumeDirMonitor() error {
	n.dirChangeMu.Lock()
	defer n.dirChangeMu.Unlock()

	if n.dirChangeStreamRef == nil {
		return errors.New("notifier not running")
	}

	if n.dirChangeRunning.Load() {
		// multiple cpus raced to get here, waiting on mutex
		return nil
	}

	logrus.Debug("resuming dir monitor")
	err := vzf.FSEventStreamStart(n.dirChangeStreamRef)
	if err != nil {
		return fmt.Errorf("start dir monitor: %w", err)
	}

	err = vzf.FSEventStreamFlushAsync(n.dirChangeStreamRef)
	if err != nil {
		return fmt.Errorf("flush dir monitor: %w", err)
	}

	n.dirChangeRunning.Store(true)
	return nil
}

func (n *VmNotifier) StopDirMonitor() error {
	n.dirChangeMu.Lock()
	defer n.dirChangeMu.Unlock()

	if n.dirChangeStreamRef == nil {
		return errors.New("notifier not running")
	}

	if !n.dirChangeRunning.Load() {
		// TODO: should never happen?
		return nil
	}

	logrus.Debug("stopping dir monitor")
	err := vzf.FSEventStreamStop(n.dirChangeStreamRef)
	if err != nil {
		return fmt.Errorf("stop dir monitor: %w", err)
	}

	n.dirChangeRunning.Store(false)
	return nil
}

// exported for rsvm
func (n *VmNotifier) OnVirtiofsActivity() {
	// called on fs hotpath
	// TODO: reentrancy risk? should be ok due to async flush... otherwise TCP conn to krpc could block on vcpu0's virtio-net IRQ, if vCPU 0 is making virtiofs HVC call to trigger FSEvents flush

	// restart and flush
	if !n.dirChangeRunning.Load() {
		err := n.ResumeDirMonitor()
		if err != nil {
			logrus.WithError(err).Error("failed to resume dir monitor")
		}
	}

	// start inactivity timer
	n.virtiofsActivityDebounce.Call()
}

func (n *VmNotifier) onVirtiofsTimeout() {
	err := n.StopDirMonitor()
	if err != nil {
		logrus.WithError(err).Error("failed to stop dir monitor")
	}
}

func (n *VmNotifier) Close() error {
	// close OK: signal select loop
	close(n.stopCh)
	n.swext.Close()
	return nil
}
