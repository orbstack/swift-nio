package main

import (
	"net"
	"net/rpc"
	"strconv"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
)

type SconInternalServer struct {
	m          *ConManager
	drmMonitor *DrmMonitor
}

func (s *SconInternalServer) Ping(_ None, _ *None) error {
	return nil
}

func (s *SconInternalServer) OnDrmResult(result drmtypes.Result, _ *None) error {
	dlog("on drm result reported")
	s.drmMonitor.dispatchResult(&result)
	return nil
}

func ListenSconInternal(m *ConManager, drmMonitor *DrmMonitor) (*SconInternalServer, error) {
	server := &SconInternalServer{
		m:          m,
		drmMonitor: drmMonitor,
	}
	rpcServer := rpc.NewServer()
	err := rpcServer.RegisterName("sci", server)
	if err != nil {
		return nil, err
	}

	listener, err := netx.Listen("tcp", net.JoinHostPort(util.DefaultAddress4().String(), strconv.Itoa(ports.GuestSconRPCInternal)))
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
