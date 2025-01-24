package portprober

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/syncx"
)

type ProbeResult struct {
	HTTPPorts  map[uint16]struct{}
	HTTPSPorts map[uint16]struct{}
}

type HostProbe struct {
	mu syncx.Mutex

	errFunc func(error)
	dialer  *net.Dialer

	host       string
	serverName string // for https

	graceTime time.Duration
	ctx       context.Context
	cancel    *util.CancelAfter

	wg          sync.WaitGroup
	activeHTTP  map[uint16]struct{}
	activeHTTPS map[uint16]struct{}

	probeResult *ProbeResult
}

type HostProbeOptions struct {
	ErrFunc func(error)
	Dialer  *net.Dialer

	Host       string
	ServerName string

	GraceTime time.Duration
}

func NewHostProbe(ctx context.Context, opts HostProbeOptions) *HostProbe {
	ctx, cancel := context.WithCancel(ctx)

	return &HostProbe{
		errFunc: opts.ErrFunc,

		dialer:     opts.Dialer,
		host:       opts.Host,
		serverName: opts.ServerName,

		graceTime: opts.GraceTime,
		ctx:       ctx,
		cancel:    util.NewTimedCancelFunc(cancel),

		activeHTTP:  make(map[uint16]struct{}),
		activeHTTPS: make(map[uint16]struct{}),

		probeResult: &ProbeResult{
			HTTPPorts:  make(map[uint16]struct{}),
			HTTPSPorts: make(map[uint16]struct{}),
		},
	}
}

func (p *HostProbe) Wait() *ProbeResult {
	p.wg.Wait()
	// safe to return probeResult because everything is done
	return p.probeResult
}

type protocolProbeFunc func(ctx context.Context, dialer *net.Dialer, host string, port uint16, serverName string) (bool, error)

func (p *HostProbe) startProtocolProbe(port uint16, probeFunc protocolProbeFunc, activeMap map[uint16]struct{}, successMap map[uint16]struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.ctx.Err() != nil {
		// don't start new probes if we're already done
		return
	}

	if _, ok := activeMap[port]; ok {
		// already probing
		return
	}

	if _, ok := successMap[port]; ok {
		// already successfully probed
		return
	}

	activeMap[port] = struct{}{}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()

		// all of these are static so safe to access from concurrent context without lock
		portOpen, err := probeFunc(p.ctx, p.dialer, p.host, port, p.serverName)
		if err != nil {
			p.errFunc(err)
		}

		p.mu.Lock()
		defer p.mu.Unlock()

		delete(activeMap, port)

		if portOpen {
			successMap[port] = struct{}{}
			p.cancel.CancelAfter(p.graceTime)
		}
	}()
}

func (p *HostProbe) startHTTPProbe(port uint16) {
	p.startProtocolProbe(port, probePortHTTP, p.activeHTTP, p.probeResult.HTTPPorts)
}

func (p *HostProbe) startHTTPSProbe(port uint16) {
	p.startProtocolProbe(port, probePortHTTPS, p.activeHTTPS, p.probeResult.HTTPSPorts)
}

func (p *HostProbe) StartProbe(port uint16) {
	p.startHTTPProbe(port)
	p.startHTTPSProbe(port)
}

func (p *HostProbe) Probe(ports map[uint16]struct{}) *ProbeResult {
	for port := range ports {
		p.StartProbe(port)
	}

	return p.Wait()
}
