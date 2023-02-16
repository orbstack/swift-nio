package main

import (
	"net"
	"net/rpc"
	"strconv"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm/sjwt"
	"github.com/kdrag0n/macvirt/scon/conf"
	"github.com/kdrag0n/macvirt/scon/killswitch"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

const (
	// 2x vmgr's start/wake grace period.
	// time cannot advance in vm faster than host. 2x gives a safe margin and can be reduced later.
	// this ensures that vmgr will check at least once (every 15 min) before our deadline is reached in terms of VM monotonic time.
	drmEventDeadline = 30 * time.Minute
)

var (
	verboseDebug = conf.Debug()
)

func dlog(msg string, args ...interface{}) {
	if verboseDebug {
		logrus.Debug(append([]interface{}{"[drm] " + msg}, args...)...)
	}
}

type DrmMonitor struct {
	conManager    *ConManager
	lastResult    *drmtypes.Result
	verifier      *sjwt.Verifier
	deadlineTimer *time.Timer
}

// scon (VM side) drm:
//
//	complex logic is all on the host side
//	every 15 min, we get the last result from vmgr and verify the token+time
func (m *DrmMonitor) Start() error {
	// killswitch
	killswitch.Watch(func(err error) {
		dlog("killswitch triggered", err)
		logrus.WithError(err).Error("build expired")
		m.conManager.pendingVMShutdown = true
		m.conManager.Close()
	})

	// set deadline
	m.deadlineTimer = time.AfterFunc(drmEventDeadline, m.onDeadlineReached)

	return nil
}

func (m *DrmMonitor) dispatchResult(result *drmtypes.Result) {
	dlog("dispatching result:", result)
	if !m.verifyResult(result) {
		logrus.Error("dispatch result - power off")
		m.conManager.pendingVMShutdown = true
		m.conManager.Close()
		return
	}

	// reset deadline timer
	if m.deadlineTimer != nil {
		m.deadlineTimer.Stop()
	}
	m.deadlineTimer = time.AfterFunc(drmEventDeadline, m.onDeadlineReached)
}

func (m *DrmMonitor) onDeadlineReached() {
	dlog("deadline reached")
	logrus.Error("deadline reached - power off")
	m.conManager.pendingVMShutdown = true
	m.conManager.Close()
}

func (m *DrmMonitor) verifyResult(result *drmtypes.Result) bool {
	if result == nil {
		dlog("fail: result = nil")
		return false
	}

	if result.State != drmtypes.StateValid {
		dlog("fail: state != valid")
		return false
	}

	// verify token
	claimInfo, err := m.verifier.Verify(result.EntitlementToken, sjwt.TokenVerifyParams{
		StrictVersion: false,
	})
	if err != nil {
		dlog("fail: verify token:", err)
		return false
	}

	if *result.ClaimInfo != *claimInfo {
		dlog("fail: claim info mismatch")
		return false
	}

	m.lastResult = result
	dlog("dispatch: ok", m.lastResult)
	return true
}

type None struct{}

type SconInternalServer struct {
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

func ListenSconInternal(drmMonitor *DrmMonitor) (*SconInternalServer, error) {
	server := &SconInternalServer{
		drmMonitor: drmMonitor,
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
