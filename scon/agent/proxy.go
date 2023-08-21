package agent

import (
	"net"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/agent/udpfwd"
)

func (a *AgentServer) StartProxyTCP(args StartProxyArgs, _ *None) error {
	spec := args.ProxySpec
	listenerFd, err := a.fdx.RecvFile(args.FdxSeq)
	if err != nil {
		return err
	}
	defer listenerFd.Close()

	listener, err := net.FileListener(listenerFd)
	if err != nil {
		return err
	}

	// Docker: always prefer v4 because Docker is traditionally v4-only
	// still try v6 in case of host net and v6-only servers
	preferV6 := spec.IsIPv6 && a.docker == nil
	proxy := tcpfwd.NewTCPProxy(listener, preferV6, spec.Port, a.localTCPRegistry, nil)
	a.tcpProxies[spec] = proxy
	go proxy.Run()

	return nil
}

func (a *AgentServer) StartProxyUDP(args StartProxyArgs, _ *None) error {
	spec := args.ProxySpec
	listenerFd, err := a.fdx.RecvFile(args.FdxSeq)
	if err != nil {
		return err
	}
	defer listenerFd.Close()

	udpConn, err := net.FilePacketConn(listenerFd)
	if err != nil {
		return err
	}

	proxy, err := udpfwd.NewUDPLocalProxy(udpConn, spec.IsIPv6, spec.Port)
	if err != nil {
		return err
	}
	a.udpProxies[spec] = proxy
	go proxy.Run()

	return nil
}

func (a *AgentServer) StopProxyTCP(args ProxySpec, _ *None) error {
	proxy, ok := a.tcpProxies[args]
	if !ok {
		return nil
	}

	proxy.Close()
	delete(a.tcpProxies, args)

	return nil
}

func (a *AgentServer) StopProxyUDP(args ProxySpec, _ *None) error {
	proxy, ok := a.udpProxies[args]
	if !ok {
		return nil
	}

	proxy.Close()
	delete(a.udpProxies, args)

	return nil
}
