package main

import (
	"net"
	"net/rpc"
	"strconv"

	"github.com/orbstack/macvirt/macvmgr/conf/ports"
	"github.com/orbstack/macvirt/scon/sgclient"
	"github.com/orbstack/macvirt/scon/util"
)

type SconGuestServer struct {
	m          *ConManager
	drmMonitor *DrmMonitor
}

func (s *SconGuestServer) Ping(_ None, _ *None) error {
	return nil
}

func (s *SconGuestServer) DockerAddNetworkBridge(config sgclient.DockerBridgeConfig, _ *None) error {
	dlog("docker add network bridge requested")
	return nil
}

func ListenSconGuest(m *ConManager, drmMonitor *DrmMonitor) (*SconGuestServer, error) {
	server := &SconGuestServer{
		m:          m,
		drmMonitor: drmMonitor,
	}
	rpcServer := rpc.NewServer()
	rpcServer.RegisterName("scg", server)

	listener, err := net.Listen("tcp", net.JoinHostPort(util.DefaultAddress4().String(), strconv.Itoa(ports.GuestSconRPCInternal)))
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
