package domainproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"

	"github.com/orbstack/macvirt/scon/bpf"
	"github.com/orbstack/macvirt/scon/domainproxy/domainproxytypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

type SSHHandler func(conn net.Conn, machineID string) error

type DomainSSHProxy struct {
	cb      ProxyCallbacks
	handler SSHHandler
}

func NewDomainSSHProxy(cb ProxyCallbacks) *DomainSSHProxy {
	return &DomainSSHProxy{cb: cb}
}

func (p *DomainSSHProxy) Start(tproxy *bpf.Tproxy, handler SSHHandler) error {
	if p.handler != nil {
		return errors.New("already started")
	}

	p.handler = handler

	ln4, err := netx.ListenTransparent(context.TODO(), "tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	ln6, err := netx.ListenTransparent(context.TODO(), "tcp", "[::1]:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	ln4RawConn, err := ln4.(syscall.Conn).SyscallConn()
	if err != nil {
		return fmt.Errorf("get rawconn from listener: %w", err)
	}
	err = util.UseRawConn(ln4RawConn, func(fd int) error {
		return tproxy.SetSock4(1, uint64(fd))
	})
	if err != nil {
		return fmt.Errorf("set tproxy socket: %w", err)
	}

	ln6RawConn, err := ln6.(syscall.Conn).SyscallConn()
	if err != nil {
		return fmt.Errorf("get rawconn from listener: %w", err)
	}
	err = util.UseRawConn(ln6RawConn, func(fd int) error {
		return tproxy.SetSock6(1, uint64(fd))
	})
	if err != nil {
		return fmt.Errorf("set tproxy socket: %w", err)
	}

	go p.serve(ln4)
	go p.serve(ln6)

	return nil
}

func (p *DomainSSHProxy) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			logrus.WithError(err).Error("failed to accept ssh connection")
			return
		}

		go func() {
			err := p.handleSSHConn(conn)
			if err != nil {
				logrus.WithError(err).Error("failed to handle ssh connection")
			}
		}()
	}
}

func (p *DomainSSHProxy) handleSSHConn(conn net.Conn) error {
	defer conn.Close()

	destAddr := conn.LocalAddr().(*net.TCPAddr)
	netAddr, ok := netip.AddrFromSlice(destAddr.IP)
	if !ok {
		return errors.New("parse dest addr")
	}

	upstream, err := p.cb.GetUpstreamByAddr(netAddr)
	if err != nil {
		return fmt.Errorf("get upstream: %w", err)
	}

	if upstream.Host.Type != domainproxytypes.HostTypeMachine {
		return errors.New("upstream is not a machine")
	}

	// attempt passthrough
	srcAddr := conn.RemoteAddr().(*net.TCPAddr)
	dialer := dialerForTransparentBind(srcAddr.IP, netconf.VmFwmarkTproxyOutboundBit)
	dialer.Timeout = probeGraceTime
	upstreamConn, err := dialer.DialContext(context.TODO(), "tcp", net.JoinHostPort(upstream.IP.String(), "22"))
	if err != nil {
		if errors.Is(err, unix.ECONNREFUSED) || errors.Is(err, unix.ETIMEDOUT) {
			// upstream doesn't have a server, so use proxy handler
			return p.handler(conn, upstream.Host.ID)
		} else {
			// some other error
			return fmt.Errorf("dial upstream: %w", err)
		}
	}
	defer upstreamConn.Close()

	// successfully dialed upstream. proxy to it
	tcppump.Pump2SpTcpTcp(conn.(*net.TCPConn), upstreamConn.(*net.TCPConn))
	return nil
}
