package fsnotify

import (
	"errors"
	"fmt"
	"sync"

	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vzf"
	"github.com/sirupsen/logrus"
)

type VmNotifier struct {
	mu      sync.Mutex
	paths   []string
	swext   *vzf.FsVmNotifier
	network *vnet.Network
	stopCh  chan struct{}
}

func NewVmNotifier(network *vnet.Network) (*VmNotifier, error) {
	swext, err := vzf.NewFsVmNotifier()
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

	conn, err := n.network.DialGuestTCPRetry(ports.GuestKrpc)
	if err != nil {
		return fmt.Errorf("dial guest: %w", err)
	}
	defer conn.Close()

	client := NewKrpcClient(conn)

	err = vzf.SwextFseventsMonitorDirs()
	if err != nil {
		return fmt.Errorf("start dir monitor: %w", err)
	}

	for {
		select {
		case buf := <-vzf.SwextFseventsKrpcEventsChan:
			err := client.WriteRaw(buf)
			if err != nil {
				logrus.WithError(err).Error("Failed to inject fsnotify events (krpc)")
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
	close(n.stopCh)
	n.swext.Close()
	return nil
}
