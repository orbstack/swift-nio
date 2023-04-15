package fsnotify

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsevents"
	"github.com/kdrag0n/macvirt/scon/isclient"
	"github.com/kdrag0n/macvirt/scon/isclient/istypes"
	"github.com/sirupsen/logrus"
)

type VmNotifier struct {
	mu            sync.Mutex
	es            *fsevents.EventStream
	isclient      atomic.Pointer[isclient.Client]
	sconClientsCh <-chan *isclient.Client
	stopCh        chan struct{}
}

func NewVmNotifier(sconClientsCh <-chan *isclient.Client) (*VmNotifier, error) {
	es := &fsevents.EventStream{
		Paths:   []string{},
		Latency: 100 * time.Millisecond,
		Flags:   fsevents.IgnoreSelf | fsevents.FileEvents,
		// fix initial restart replaying events
		EventID: uint64(0xFFFFFFFFFFFFFFFF),
	}

	return &VmNotifier{
		es:            es,
		sconClientsCh: sconClientsCh,
		stopCh:        make(chan struct{}),
	}, nil
}

func convertEventBatch(events []fsevents.Event) istypes.FsnotifyEventsBatch {
	paths := make([]string, 0, len(events))
	flags := make([]istypes.FsnotifyEventFlags, 0, len(events))

	for _, event := range events {
		flgs := istypes.FsnotifyEventFlags(0)
		if event.Flags&fsevents.ItemCreated != 0 {
			flgs |= istypes.FsnotifyEventCreate
		}
		if event.Flags&fsevents.ItemRemoved != 0 {
			flgs |= istypes.FsnotifyEventRemove
		}
		//TODO renamed
		if event.Flags&fsevents.ItemModified != 0 {
			flgs |= istypes.FsnotifyEventModify
		}
		// no finder info
		if event.Flags&(fsevents.ItemInodeMetaMod|fsevents.ItemChangeOwner|fsevents.ItemXattrMod) != 0 {
			flgs |= istypes.FsnotifyEventStatAttr
		}

		// ignore HistoryDone, etc
		if flgs != 0 {
			paths = append(paths, event.Path)
			flags = append(flags, flgs)
		}
	}

	return istypes.FsnotifyEventsBatch{
		Paths: paths,
		Flags: flags,
	}
}

func (n *VmNotifier) SetIsclient(isclient *isclient.Client) {
	n.isclient.Store(isclient)
}

func (n *VmNotifier) Run() error {
	n.es.Start()

	for {
		select {
		case events := <-n.es.Events:
			batch := convertEventBatch(events)

			client := n.isclient.Load()
			if client == nil {
				continue
			}

			err := client.InjectFsnotifyEvents(batch)
			if err != nil {
				logrus.WithError(err).Error("Failed to inject fsnotify events")
			}
		case client := <-n.sconClientsCh:
			n.SetIsclient(client)
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
