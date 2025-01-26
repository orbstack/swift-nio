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

type LtypeFlags uint8

const (
	LtypeTCP LtypeFlags = 1 << iota
	LtypeUDP
	LtypeIPTables

	LtypeAll = LtypeTCP | LtypeUDP | LtypeIPTables
)

type PmonEvent struct {
	DirtyFlags  LtypeFlags
	NetnsCookie uint64
}

type Pmon struct {
	mu       syncx.Mutex
	pmonObjs *pmonObjects
	reader   *ringbuf.Reader

	closers []io.Closer
}

func (p *Pmon) attachOneCgLocked(typ ebpf.AttachType, prog *ebpf.Program, cgroupPath string) error {
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

func (p *Pmon) attachOneKretprobeLocked(prog *ebpf.Program, fnName string) error {
	l, err := link.Kretprobe(fnName, prog, nil)
	if err != nil {
		return fmt.Errorf("attach: %w", err)
	}
	p.closers = append(p.closers, l)
	return nil
}

func (p *Pmon) Close() error {
	for _, closer := range p.closers {
		closer.Close()
	}
	return nil
}

func NewPmon(netnsCookie uint64) (*Pmon, error) {
	pmon := &Pmon{}

	spec, err := loadPmon()
	if err != nil {
		return nil, fmt.Errorf("load spec: %w", err)
	}

	err = spec.RewriteConstants(map[string]any{
		"config_netns_cookie": netnsCookie,
	})
	if err != nil {
		return nil, fmt.Errorf("configure: %w", err)
	}

	objs := pmonObjects{}
	err = spec.LoadAndAssign(&objs, nil)
	if err != nil {
		return nil, fmt.Errorf("load objs: %w", err)
	}
	pmon.closers = append(pmon.closers, &objs)
	pmon.pmonObjs = &objs

	return pmon, nil
}

func (p *Pmon) Attach(cgroupPath string) error {
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

	err = p.attachOneKretprobeLocked(p.pmonObjs.NfTablesNewrule, "nf_tables_newrule")
	if err != nil {
		return err
	}
	err = p.attachOneKretprobeLocked(p.pmonObjs.NfTablesDelrule, "nf_tables_delrule")
	if err != nil {
		return err
	}

	reader, err := ringbuf.NewReader(p.pmonObjs.NotifyRing)
	if err != nil {
		return fmt.Errorf("create reader: %w", err)
	}
	p.reader = reader

	return nil
}

func (p *Pmon) deserializeRecord(rec *ringbuf.Record) PmonEvent {
	ev := PmonEvent{
		DirtyFlags:  LtypeFlags(rec.RawSample[0]),
		NetnsCookie: binary.LittleEndian.Uint64(rec.RawSample[1:]),
	}
	return ev
}

func (p *Pmon) Monitor(fn func(PmonEvent) error) error {
	var rec ringbuf.Record
	for {
		err := p.reader.ReadInto(&rec)
		if err != nil {
			if errors.Is(err, os.ErrClosed) {
				return nil
			} else {
				return fmt.Errorf("read: %w", err)
			}
		}

		ev := p.deserializeRecord(&rec)

		err = fn(ev)
		if err != nil {
			logrus.WithError(err).Error("pmon callback failed")
		}
	}
}
