package fsnotify

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fsnotify/fsevents"
	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/sirupsen/logrus"
)

type VmNotifier struct {
	mu      sync.Mutex
	es      *fsevents.EventStream
	network *vnet.Network
	stopCh  chan struct{}
}

type eventBatch struct {
	Paths []string
	Descs []uint64
}

func NewVmNotifier(network *vnet.Network) (*VmNotifier, error) {
	es := &fsevents.EventStream{
		Paths:   []string{},
		Latency: 100 * time.Millisecond,
		Flags:   fsevents.IgnoreSelf | fsevents.NoDefer | fsevents.FileEvents,
		// fix initial restart replaying events
		EventID: uint64(0xFFFFFFFFFFFFFFFF),
	}

	return &VmNotifier{
		es:      es,
		network: network,
		stopCh:  make(chan struct{}),
	}, nil
}

func convertEventBatch(events []fsevents.Event) eventBatch {
	paths := make([]string, 0, len(events))
	descs := make([]uint64, 0, len(events))

	for _, event := range events {
		var flgs uint64
		if event.Flags&fsevents.ItemCreated != 0 {
			flgs |= npFlagCreate
		}
		if event.Flags&fsevents.ItemRemoved != 0 {
			flgs |= npFlagRemove
		}
		//TODO renamed
		if event.Flags&fsevents.ItemModified != 0 {
			flgs |= npFlagModify
		}
		// no finder info
		if event.Flags&(fsevents.ItemInodeMetaMod|fsevents.ItemChangeOwner|fsevents.ItemXattrMod) != 0 {
			flgs |= npFlagStatAttr
		}

		// ignore HistoryDone, etc
		if flgs != 0 && len(event.Path) <= linuxPathMax {
			// prefix all the paths here
			newPath := mounts.Virtiofs + event.Path
			paths = append(paths, newPath)
			descs = append(descs, flgs|uint64(len(newPath))<<32)
		}
	}

	return eventBatch{
		Paths: paths,
		Descs: descs,
	}
}

func (n *VmNotifier) Run() error {
	n.es.Start()

	conn, err := n.network.DialGuestTCPRetry(ports.GuestKrpc)
	if err != nil {
		return fmt.Errorf("dial guest: %w", err)
	}
	defer conn.Close()

	client := NewKrpcClient(conn)
	for {
		select {
		case events := <-n.es.Events:
			batch := convertEventBatch(events)

			err := client.NotifyproxyInject(batch)
			if err != nil {
				logrus.WithError(err).Error("Failed to inject fsnotify events")
			}
		case <-n.stopCh:
			return nil
		}
	}
}

func (n *VmNotifier) Add(path string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.es.Paths = append(n.es.Paths, path)
	n.es.Restart()

	return nil
}

func (n *VmNotifier) Remove(path string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	for i, p := range n.es.Paths {
		if p == path {
			n.es.Paths = append(n.es.Paths[:i], n.es.Paths[i+1:]...)
			n.es.Restart()
			return nil
		}
	}

	return errors.New("path not found")
}

func (n *VmNotifier) Close() error {
	close(n.stopCh)
	n.es.Stop()
	return nil
}
