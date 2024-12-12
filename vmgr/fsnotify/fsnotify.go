package fsnotify

import (
	"context"
	"errors"
	"fmt"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	swext "github.com/orbstack/macvirt/vmgr/swext"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vnet/services/readyevents/readyclient"
	"github.com/sirupsen/logrus"
)

type VmNotifier struct {
	mu      syncx.Mutex
	paths   []string
	swext   *swext.FsVmNotifier
	network *vnet.Network
	stopCh  chan struct{}
}

func NewVmNotifier(network *vnet.Network) (*VmNotifier, error) {
	swext, err := swext.NewFsVmNotifier()
	if err != nil {
		return nil, fmt.Errorf("create swext: %w", err)
	}

	return &VmNotifier{
		swext:   swext,
		network: network,
		stopCh:  make(chan struct{}),
	}, nil
}

func (n *VmNotifier) Run() error {
	n.swext.Start()

	conn, err := n.network.WaitDialGuestTCP(context.TODO(), readyclient.ServiceKrpc, ports.GuestKrpc)
	if err != nil {
		return fmt.Errorf("dial guest: %w", err)
	}
	defer conn.Close()

	client := NewKrpcClient(conn)

	err = swext.FseventsMonitorDirs()
	if err != nil {
		return fmt.Errorf("start dir monitor: %w", err)
	}

	for {
		select {
		case buf := <-swext.FseventsKrpcEventsChan:
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

func (n *VmNotifier) Close() error {
	// close OK: signal select loop
	close(n.stopCh)
	n.swext.Close()
	return nil
}
