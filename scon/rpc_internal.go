package main

import (
	"net"
	"net/rpc"
	"strconv"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/scon/isclient/istypes"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

type SconInternalServer struct {
	m                *ConManager
	drmMonitor       *DrmMonitor
	fsnotifyInjector *fsnotifyInjector
}

func (s *SconInternalServer) Ping(_ None, _ *None) error {
	return nil
}

func (s *SconInternalServer) OnDrmResult(result drmtypes.Result, _ *None) error {
	dlog("on drm result reported")
	s.drmMonitor.dispatchResult(&result)
	return nil
}

func (s *SconInternalServer) OnNfsMounted(_ None, _ *None) error {
	logrus.Debug("NFS mounted on host, binding into containers")
	return s.m.onHostNfsMounted()
}

func (s *SconInternalServer) InjectFsnotifyEvents(events istypes.FsnotifyEventsBatch, _ *None) error {
	logrus.WithField("events", events).Debug("injecting fsnotify events")
	return s.fsnotifyInjector.Inject(events)
}

func ListenSconInternal(m *ConManager, drmMonitor *DrmMonitor) (*SconInternalServer, error) {
	server := &SconInternalServer{
		m:                m,
		drmMonitor:       drmMonitor,
		fsnotifyInjector: newFsnotifyInjector(),
	}
	rpcServer := rpc.NewServer()
	rpcServer.RegisterName("sci", server)

	listener, err := net.Listen("tcp", net.JoinHostPort(util.DefaultAddress4().String(), strconv.Itoa(ports.GuestSconRPCInternal)))
	if err != nil {
		return nil, err
	}

	go func() {
		rpcServer.Accept(listener)
	}()

	return server, nil
}
