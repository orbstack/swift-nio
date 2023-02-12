package main

import (
	"errors"
	"net"
	"net/rpc"
	"strconv"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm/sjwt"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

const (
	drmCheckInterval = 15 * time.Minute
	// long enough to include all retries and 30s timeouts in vmgr
	drmCheckTimeout = 8 * time.Minute
)

type DrmMonitor struct {
	conManager *ConManager
	lastResult *drmtypes.Result
	verifier   *sjwt.Verifier
}

func withTimeout[T any](fn func() (T, error), timeout time.Duration) (T, error) {
	done := make(chan struct{})
	defer close(done)

	var result T
	var err error
	go func() {
		result, err = fn()
		select {
		case done <- struct{}{}:
		default:
		}
	}()

	select {
	case <-done:
		return result, err
	case <-time.After(timeout):
		return result, errors.New("timeout")
	}
}

func (m *DrmMonitor) Run() error {
	ticker := time.NewTicker(drmCheckInterval)
	defer ticker.Stop()

	go func() {
		result, err := withTimeout(func() (*drmtypes.Result, error) {
			return m.conManager.host.GetLastDrmResult()
		}, drmCheckTimeout)
		if err != nil {
			logrus.WithError(err).Error("failed to get initial last result")
		}

		if result != nil {
			m.lastResult = result
			m.dispatchResult(m.lastResult)
		}
	}()

	for range ticker.C {
		// timeout prevents malicious server from blocking and bypassing DRM
		result, err := withTimeout(func() (*drmtypes.Result, error) {
			return m.conManager.host.GetLastDrmResult()
		}, drmCheckTimeout)
		if err != nil {
			logrus.WithError(err).Error("failed to get new result")
		}

		if result != nil {
			m.lastResult = result
		}
		m.dispatchResult(m.lastResult)
	}

	return nil
}

func (m *DrmMonitor) dispatchResult(result *drmtypes.Result) {
	if !m.verifyResult(result) {
		logrus.Error("dispatch result: power off")
		m.conManager.pendingVMShutdown = true
		m.conManager.Close()
	}
}

func (m *DrmMonitor) verifyResult(result *drmtypes.Result) bool {
	if result == nil {
		return false
	}

	if result.State != drmtypes.StateValid {
		return false
	}

	// verify token
	claimInfo, err := m.verifier.Verify(result.EntitlementToken, sjwt.TokenVerifyParams{
		StrictVersion: false,
	})
	if err != nil {
		return false
	}

	if *result.ClaimInfo != *claimInfo {
		return false
	}

	m.lastResult = &drmtypes.Result{
		State:            drmtypes.StateValid,
		EntitlementToken: result.EntitlementToken,
		RefreshToken:     result.RefreshToken,
		ClaimInfo:        claimInfo,
	}
	return true
}

func (m *DrmMonitor) LastResult() *drmtypes.Result {
	return m.lastResult
}

type None struct{}

type SconInternalServer struct {
	drmMonitor *DrmMonitor
}

func (s *SconInternalServer) Ping(_ None, _ *None) error {
	return nil
}

func (s *SconInternalServer) OnDrmResult(result drmtypes.Result, _ *None) error {
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
