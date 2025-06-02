package main

import (
	"fmt"
	"os"
	"time"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/scon/killswitch"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/drm/sjwt"
	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
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

	initRestored *syncx.CondBool
}

// scon (VM side) drm:
//
//	complex logic is all on the host side
//	every 15 min, we get the last result from vmgr and verify the token+time
func (m *DrmMonitor) Start() error {
	// killswitch
	killswitch.Watch(func(err error) {
		dlog("killswitch triggered", err)
		logrus.Error(err.Error())
		requestSystemPoweroff()
	})

	// set deadline
	m.deadlineTimer = time.AfterFunc(drmEventDeadline, m.onDeadlineReached)

	// prepopulate drm result to get correct license status on start
	result, err := m.conManager.host.GetLastDrmResult()
	if err != nil {
		if err.Error() == "no result" {
			result = nil
		} else {
			return fmt.Errorf("get last drm result: %w", err)
		}
	}

	if result != nil {
		m.dispatchResult(result)
	}

	m.initRestored.Set(true)
	return nil
}

func (m *DrmMonitor) dispatchResult(result *drmtypes.Result) {
	dlog("dispatching result:", result)
	if !m.verifyResult(result) {
		logrus.Error("dispatch result - power off")
		requestSystemPoweroff()
		return
	}

	// reset deadline timer
	// for now we only require one valid result on start, then never need it again
	// periodic is ok on ARM because monotonic VM time doesn't advance during sleep
	// but on x86 it does, and pausing causes nfs timeouts with both vsock and tcp
	// so no choice here. architecture-dependent drm is bad idea
	// TODO: more strict? require periodic?
	if m.deadlineTimer != nil {
		m.deadlineTimer.Stop()
		m.deadlineTimer = nil
	}
}

func (m *DrmMonitor) onDeadlineReached() {
	dlog("deadline reached")
	logrus.Error("deadline reached - power off")
	requestSystemPoweroff()
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
		dlog("first token verify failed. retrying")

		// strange: if vmgr thinks the token is valid, then scon shouldn't think it's invalid.
		// usually this is caused by a time sync issue, so force vinit to step the time and retry
		err2 := m.conManager.vinit.Post("internal/sync_time", nil, nil)
		if err2 != nil {
			logrus.WithError(err2).Error("failed to sync time")
			dlog("fail: step time: ", err2, " - verify token:", err)
			return false
		}

		// time should now be fixed, so retry verification
		claimInfo, err = m.verifier.Verify(result.EntitlementToken, sjwt.TokenVerifyParams{
			StrictVersion: false,
		})
		if err != nil {
			dlog("fail: verify token:", err)
			return false
		}
		dlog("second token verify ok")
	}

	// update the claim info that we save
	result.ClaimInfo = claimInfo
	m.lastResult = result
	dlog("dispatch: ok", m.lastResult)
	return true
}

func (m *DrmMonitor) isLicensed() bool {
	m.initRestored.Wait()
	return m.lastResult != nil && m.lastResult.ClaimInfo.EntitlementTier != drmtypes.EntitlementTierNone
}

type None struct{}

func requestSystemPoweroff() {
	// poweroff: send SIGUSR2 to init
	logrus.Info("requesting poweroff")
	err := unix.Kill(1, unix.SIGUSR2)
	if err != nil {
		logrus.WithError(err).Error("failed to request poweroff")
		os.Exit(1)
	}
}
