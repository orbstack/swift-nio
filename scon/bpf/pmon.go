package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
)

type zero struct {
	_ uint8
}

type ListenerTypeFlags uint8

const (
	ListenerTypeTCP ListenerTypeFlags = 1 << iota
	ListenerTypeUDP
	ListenerTypeIPTables

	ListenerTypeAll = ListenerTypeTCP | ListenerTypeUDP | ListenerTypeIPTables
)

type PortMonitorEvent struct {
	DirtyFlags  ListenerTypeFlags
	NetnsCookie uint64
}

type PortMonitor struct {
	mu       syncx.Mutex
	pmonObjs *pmonObjects
	reader   *ringbuf.Reader

	closers []io.Closer

	netnsCookieInterestedIDs map[uint64]map[string]struct{}

	globalCallbacks map[string]func(PortMonitorEvent)

	netnsCallbacksCookies map[string]uint64
	netnsCallbacks        map[uint64]map[string]func(PortMonitorEvent)
}

func NewPmon() (*PortMonitor, error) {
	pmon := &PortMonitor{
		netnsCookieInterestedIDs: make(map[uint64]map[string]struct{}),

		globalCallbacks: make(map[string]func(PortMonitorEvent)),

		netnsCallbacksCookies: make(map[string]uint64),
		netnsCallbacks:        make(map[uint64]map[string]func(PortMonitorEvent)),
	}

	spec, err := loadPmon()
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}

	objs := pmonObjects{}
	err = spec.LoadAndAssign(&objs, nil)
	if err != nil {
		return nil, fmt.Errorf("load objs: %w", err)
	}
	pmon.closers = append(pmon.closers, &objs)
	pmon.pmonObjs = &objs

	pmon.reader, err = ringbuf.NewReader(pmon.pmonObjs.NotifyRing)
	if err != nil {
		return nil, fmt.Errorf("create reader: %w", err)
	}

	go pmon.monitor()

	return pmon, nil
}

func (p *PortMonitor) deserializeRecord(rec *ringbuf.Record) PortMonitorEvent {
	ev := PortMonitorEvent{
		DirtyFlags:  ListenerTypeFlags(rec.RawSample[8]),
		NetnsCookie: binary.NativeEndian.Uint64(rec.RawSample[0:]),
	}
	return ev
}

func (p *PortMonitor) monitor() {
	var rec ringbuf.Record
	for {
		err := p.reader.ReadInto(&rec)
		if err != nil {
			if !errors.Is(err, os.ErrClosed) {
				logrus.WithError(err).Error("pmon read failed")
			}
			return
		}

		ev := p.deserializeRecord(&rec)

		p.mu.Lock()
		for _, callback := range p.globalCallbacks {
			go callback(ev)
		}

		if callbacks, ok := p.netnsCallbacks[ev.NetnsCookie]; ok {
			for _, callback := range callbacks {
				go callback(ev)
			}
		}
		p.mu.Unlock()
	}
}

func (p *PortMonitor) Close() error {
	for _, closer := range p.closers {
		closer.Close()
	}
	return nil
}

func (p *PortMonitor) attachOneCgLocked(typ ebpf.AttachType, prog *ebpf.Program, cgroupPath string) error {
	l, err := link.AttachCgroup(link.CgroupOptions{
		Path:    cgroupPath,
		Attach:  typ,
		Program: prog,
	})
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}

	p.closers = append(p.closers, l)
	return nil
}

func (p *PortMonitor) attachOneKretprobeLocked(prog *ebpf.Program, fnName string) error {
	l, err := link.Kretprobe(fnName, prog, nil)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}

	p.closers = append(p.closers, l)
	return nil
}

func (p *PortMonitor) AttachCgroup(cgroupPath string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	err := p.attachOneCgLocked(ebpf.AttachCGroupInet4PostBind, p.pmonObjs.PmonPostBind4, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCGroupInet4Connect, p.pmonObjs.PmonConnect4, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCGroupUDP4Recvmsg, p.pmonObjs.PmonRecvmsg4, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCGroupUDP4Sendmsg, p.pmonObjs.PmonSendmsg4, cgroupPath)
	if err != nil {
		return err
	}

	err = p.attachOneCgLocked(ebpf.AttachCGroupInet6PostBind, p.pmonObjs.PmonPostBind6, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCGroupInet6Connect, p.pmonObjs.PmonConnect6, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCGroupUDP6Recvmsg, p.pmonObjs.PmonRecvmsg6, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCGroupUDP6Sendmsg, p.pmonObjs.PmonSendmsg6, cgroupPath)
	if err != nil {
		return err
	}
	err = p.attachOneCgLocked(ebpf.AttachCgroupInetSockRelease, p.pmonObjs.PmonSockRelease, cgroupPath)
	if err != nil {
		return err
	}

	return nil
}

func (p *PortMonitor) AttachKretprobe() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	err := p.attachOneKretprobeLocked(p.pmonObjs.NfTablesNewrule, "nf_tables_newrule")
	if err != nil {
		return err
	}
	err = p.attachOneKretprobeLocked(p.pmonObjs.NfTablesDelrule, "nf_tables_delrule")
	if err != nil {
		return err
	}

	return nil
}

func (p *PortMonitor) addNetns(netnsCookie uint64) error {
	return p.pmonObjs.NetnsCookies.Put(netnsCookie, zero{})
}

func (p *PortMonitor) removeNetns(netnsCookie uint64) error {
	return p.pmonObjs.NetnsCookies.Delete(netnsCookie)
}

func (p *PortMonitor) registerNetnsInterestLocked(id string, netnsCookie uint64) error {
	if _, ok := p.netnsCookieInterestedIDs[netnsCookie]; !ok || len(p.netnsCookieInterestedIDs[netnsCookie]) == 0 {
		// first interest, make a slice and add netns
		p.netnsCookieInterestedIDs[netnsCookie] = make(map[string]struct{})

		err := p.addNetns(netnsCookie)
		if err != nil {
			return err
		}
	}
	p.netnsCookieInterestedIDs[netnsCookie][id] = struct{}{}

	return nil
}

func (p *PortMonitor) RegisterNetnsInterest(id string, netnsCookie uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.registerNetnsInterestLocked(id, netnsCookie)
}

func (p *PortMonitor) deregisterNetnsInterest(id string, netnsCookie uint64) error {
	interested, ok := p.netnsCookieInterestedIDs[netnsCookie]
	if !ok {
		return fmt.Errorf("interest for netns cookie not found: %v", netnsCookie)
	}

	delete(interested, id)

	if len(interested) == 0 {
		delete(p.netnsCookieInterestedIDs, netnsCookie)
		return p.removeNetns(netnsCookie)
	} else {
		return nil
	}
}

func (p *PortMonitor) DeregisterNetnsInterest(id string, netnsCookie uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.deregisterNetnsInterest(id, netnsCookie)
}

func (p *PortMonitor) AddCallback(id string, netnsCookie uint64, fn func(PortMonitorEvent)) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.netnsCallbacksCookies[id]; ok {
		return fmt.Errorf("callback already exists: %v", id)
	}
	p.netnsCallbacksCookies[id] = netnsCookie

	if _, ok := p.netnsCallbacks[netnsCookie]; !ok {
		p.netnsCallbacks[netnsCookie] = make(map[string]func(PortMonitorEvent))
	}
	p.netnsCallbacks[netnsCookie][id] = fn

	return p.registerNetnsInterestLocked(id, netnsCookie)
}

func (p *PortMonitor) RemoveCallback(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	netnsCookie, ok := p.netnsCallbacksCookies[id]
	if !ok {
		return fmt.Errorf("callback not found: %v", id)
	}
	delete(p.netnsCallbacksCookies, id)

	delete(p.netnsCallbacks[netnsCookie], id)
	if len(p.netnsCallbacks[netnsCookie]) == 0 {
		delete(p.netnsCallbacks, netnsCookie)
	}

	return p.deregisterNetnsInterest(id, netnsCookie)
}

func (p *PortMonitor) ClearCallbacks(netnsCookie uint64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for id := range p.netnsCallbacks[netnsCookie] {
		err := p.deregisterNetnsInterest(id, netnsCookie)
		if err != nil {
			return err
		}
	}

	delete(p.netnsCallbacks, netnsCookie)

	return nil
}

func (p *PortMonitor) AddGlobalCallback(id string, fn func(PortMonitorEvent)) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.globalCallbacks[id]; ok {
		return fmt.Errorf("callback already exists: %v", id)
	}
	p.globalCallbacks[id] = fn

	return nil
}

func (p *PortMonitor) RemoveGlobalCallback(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.globalCallbacks[id]; !ok {
		return fmt.Errorf("callback not found: %v", id)
	}
	delete(p.globalCallbacks, id)

	return nil
}

func (p *PortMonitor) ClearGlobalCallbacks() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.globalCallbacks = make(map[string]func(PortMonitorEvent))
}
