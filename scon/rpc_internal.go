package main

import (
	"net"
	"net/rpc"
	"strconv"

	"github.com/miekg/dns"
	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
)

type SconInternalServer struct {
	m             *ConManager
	dockerMachine *Container
	drmMonitor    *DrmMonitor
}

func (s *SconInternalServer) Ping(_ None, _ *None) error {
	return nil
}

func (s *SconInternalServer) OnDrmResult(result drmtypes.Result, _ *None) error {
	dlog("on drm result reported")
	s.drmMonitor.dispatchResult(&result)
	return nil
}

func (s *SconInternalServer) OnVmconfigUpdate(config *vmconfig.VmConfig, _ *None) error {
	dlog("on vmconfig update reported")
	s.m.vmConfig = config

	// if needed, update docker agent state
	if s.dockerMachine.Running() {
		err := s.dockerMachine.UseAgent(func(a *agent.Client) error {
			return a.DockerOnVmconfigUpdate(config)
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (s *SconInternalServer) MdnsHandleQuery(q dns.Question, rrs *[]dns.RR) error {
	dlog("mdns handle query reported")
	*rrs = s.m.net.mdnsRegistry.handleQuery(q)
	return nil
}

func ListenSconInternal(m *ConManager, drmMonitor *DrmMonitor) (*SconInternalServer, error) {
	dockerMachine, err := m.GetByID(ContainerIDDocker)
	if err != nil {
		return nil, err
	}

	server := &SconInternalServer{
		m:             m,
		dockerMachine: dockerMachine,
		drmMonitor:    drmMonitor,
	}
	rpcServer := rpc.NewServer()
	err = rpcServer.RegisterName("sci", server)
	if err != nil {
		return nil, err
	}

	listener, err := netx.Listen("tcp", net.JoinHostPort(vnetGuestIP4.String(), strconv.Itoa(ports.GuestSconRPCInternal)))
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
