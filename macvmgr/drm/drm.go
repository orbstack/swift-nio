package drm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appver"
	"github.com/kdrag0n/macvirt/macvmgr/conf/ports"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm/sjwt"
	"github.com/kdrag0n/macvirt/macvmgr/drm/timex"
	"github.com/kdrag0n/macvirt/macvmgr/drm/updates"
	"github.com/kdrag0n/macvirt/macvmgr/syncx"
	"github.com/kdrag0n/macvirt/macvmgr/vclient/iokit"
	"github.com/kdrag0n/macvirt/macvmgr/vnet"
	"github.com/kdrag0n/macvirt/scon/isclient"
	"github.com/sirupsen/logrus"
)

const (
	// evaluate the local DRM state this often
	evaluateInterval = 15 * time.Minute
	// wait at least this long before sending another server checkin request
	// it should be 24h (1 day) but since ppl don't wake up at the same time every day,
	// we use a shorter interval to allow for faster recovery in case of network errors.
	// it's 24h-30m (2*interval)
	checkinLifetime = 24*time.Hour - 2*evaluateInterval

	// allow non-explicit (i.e. network error) failures up to this long after startup
	startGracePeriod = 15 * time.Minute
	// wait this long for VM to stop if DRM failed
	FailStopTimeout = 2 * time.Minute

	// after VM wakes up from sleep, wait this long for time/clock sync before reporting
	// this ensures host and VM clock are synced again
	// this is 4 burst reqs * 2 secs interval * 2x syncs + margin = 30 secs
	sleepSyncPeriod = 30 * time.Second

	// temporary preview refresh token
	previewRefreshToken = "1181201e-23f8-41f6-9660-b7110f4bfedb"

	apiBaseUrlProd = "https://api-license.orbstack.dev"
	apiBaseUrlDev  = "http://localhost:8400"
)

var (
	verboseDebug = conf.Debug()

	// retry delays for DRM checkin requests
	retryDelays = []time.Duration{0, 5 * time.Second, 30 * time.Second, 5 * time.Minute}
)

var (
	onceClient syncx.Once[*DrmClient]

	ErrVerify = errors.New("verification failed")
	ErrSleep  = errors.New("sleep")
	ErrGrace  = errors.New("grace")
)

type DrmClient struct {
	mu         sync.Mutex
	checkMu    sync.Mutex
	verifier   *sjwt.Verifier
	http       *http.Client
	apiBaseURL string

	state      drmtypes.State
	lastResult *drmtypes.Result

	refreshToken string
	identifiers  *drmtypes.Identifiers
	appVersion   drmtypes.AppVersion
	startTime    timex.MonoSleepTime

	// late init
	vnet            *vnet.Network
	sconInternal    *isclient.Client
	sconHasReported bool

	updater *updates.Updater

	failChan chan struct{}
}

func newDrmClient() *DrmClient {
	ids, err := deriveIdentifiers()
	if err != nil {
		panic(err)
	}

	ver := appver.Get()
	appVersion := drmtypes.AppVersion{
		Code: ver.Code,
		Git:  ver.GitCommit,
	}

	baseURL := apiBaseUrlProd
	if conf.Debug() {
		baseURL = apiBaseUrlDev
	}

	c := &DrmClient{
		// start in permissive valid state
		state:    drmtypes.StateValid,
		verifier: sjwt.NewVerifier(ids, appVersion),
		/*http below*/
		apiBaseURL: baseURL,

		lastResult: nil,

		//TODO accounts
		refreshToken: previewRefreshToken,
		identifiers:  ids,
		appVersion:   appVersion,
		startTime:/*mono*/ timex.NowMonoSleep(),

		updater: updates.NewUpdater(),

		failChan: make(chan struct{}),
	}

	c.http = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    3,
			IdleConnTimeout: 60 * time.Second,
			Proxy: func(*http.Request) (*url.URL, error) {
				vnetwork := c.vnet
				if vnetwork == nil {
					return nil, nil
				}

				// use same proxy as VM network
				proxy := vnetwork.Proxy.GetHTTPSProxyURL()
				dlog("using proxy: ", proxy)
				return proxy, nil
			},
		},
	}

	return c
}

func (c *DrmClient) State() drmtypes.State {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.state
}

func (c *DrmClient) Valid() bool {
	return c.State() == drmtypes.StateValid
}

func (c *DrmClient) FailChan() <-chan struct{} {
	return c.failChan
}

func (c *DrmClient) setState(state drmtypes.State) {
	c.state = state
	dlog("set state: ", state)
	if state == drmtypes.StateInvalid {
		dlog("invalid state, dispatching fail event")
		close(c.failChan)
	}
}

func (c *DrmClient) LastResult() *drmtypes.Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lastResult
}

func (c *DrmClient) UpdateResult() (*drmtypes.Result, error) {
	return c.KickCheck()
}

func (c *DrmClient) Run() {
	// sleep for a few sec to allow for proxy settings to load
	time.Sleep(3 * time.Second)

	ticker := time.NewTicker(evaluateInterval)
	defer ticker.Stop()

	for ; true; <-ticker.C {
		dlog("periodic/init check")
		result, err := c.KickCheck()
		if err != nil {
			dlog("periodic check failed: ", err)
			// only log in release if we got something (bad/good)
			if result != nil && !errors.Is(err, ErrSleep) && !errors.Is(err, ErrGrace) {
				logrus.WithError(err).Error("check failed")
			}
			// and don't log if we got none back
			if result == nil {
				continue
			}
		}

		dlog("periodic check result: ", result)
	}
}

func (c *DrmClient) SetVnet(n *vnet.Network) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.vnet = n
}

func (c *DrmClient) reportToScon(result *drmtypes.Result) error {
	if c.vnet == nil {
		dlog("missing vnet, skip report")
		return errors.New("missing vnet")
	}

	// if we just woke up from sleep, give the VM time to sync clock
	// (if we got here, we must be awake and must have a valid result)
	wakeTime := iokit.LastWakeTime
	if wakeTime != nil && /*mono*/ timex.SinceMonoSleep(*wakeTime) < sleepSyncPeriod {
		diff := sleepSyncPeriod - /*mono*/ timex.SinceMonoSleep(*wakeTime)
		dlog("waiting for sleep-wake time sync", diff)
		time.Sleep(diff)
	}

	// after initial report, only report if state change (invalid), because
	// scon only requires initial valid report now on start
	// in debug, don't do this because I restart scon in dev
	// TODO remove if we do perpetual periodic checks again
	/*if c.sconHasReported && result.State == drmtypes.StateValid {
		dlog("already reported to scon, skip")
		return nil
	}*/

	// report
	dlog("report to scon internal")
	err := c.UseSconInternalClient(func(scon *isclient.Client) error {
		return scon.OnDrmResult(result)
	})
	if err != nil {
		logrus.WithError(err).Error("failed to report to scon internal")
		return err
	}
	c.sconHasReported = true
	dlog("report done")

	return nil
}

// TODO move out of drm client
func (c *DrmClient) UseSconInternalClient(fn func(*isclient.Client) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sconInternal == nil || c.sconInternal.Ping() != nil {
		if c.sconInternal != nil {
			logrus.Info("reconnecting to scon internal rpc")
			c.sconInternal.Close()
		}

		// connect
		dlog("dial scon internal rpc")
		// important: retry. if it fails, drm could fail when it shouldn't, and ~/OrbStack bind mounts won't work
		conn, err := c.vnet.DialGuestTCPRetry(ports.GuestSconRPCInternal)
		if err != nil {
			return err
		}
		sconInternal, err := isclient.New(conn)
		if err != nil {
			return err
		}
		c.sconInternal = sconInternal
	}

	return fn(c.sconInternal)
}

func (c *DrmClient) KickCheck() (*drmtypes.Result, error) {
	dlog("kick check")

	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	lastResult := c.LastResult()
	dlog("last result: ", lastResult)
	if lastResult != nil && lastResult.State == drmtypes.StateValid && /*mono*/ timex.SinceMonoSleep(lastResult.CheckedAt) < checkinLifetime && /*wall*/ time.Now().Before(lastResult.ClaimInfo.ExpiresAt.Add(sjwt.NotAfterLeeway)) {
		dlog("skipping checkin due to valid result")
		return lastResult, nil
	}

	if iokit.IsAsleep() {
		dlog("skipping checkin due to sleep")
		return nil, ErrSleep
	}

	if !c.Valid() {
		dlog("skipping checkin due to invalid state")
		return nil, errors.New("invalid state")
	}

	result, err := c.doCheckinLockedRetry()
	if err != nil {
		isVerifyFail := errors.Is(err, ErrVerify)
		wakeTime := iokit.LastWakeTime

		// new check failed. are we in grace period for old token expiry?
		if !isVerifyFail && lastResult != nil && /*wall*/ time.Now().Before(lastResult.ClaimInfo.ExpiresAt.Add(sjwt.NotAfterLeeway)) {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in last token grace period")
			return lastResult, nil
		} else if !isVerifyFail && lastResult == nil && /*mono*/ timex.SinceMonoSleep(c.startTime) < startGracePeriod {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in start grace period")
			return nil, errors.Join(err, ErrGrace)
		} else if !isVerifyFail && lastResult == nil && wakeTime != nil && /*mono*/ timex.SinceMonoSleep(*wakeTime) < startGracePeriod {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in wake grace period")
			return nil, errors.Join(err, ErrGrace)
		} else {
			// no grace period (or verify failed), so invalidate the result
			result = &drmtypes.Result{
				State:            drmtypes.StateInvalid,
				EntitlementToken: "",
				RefreshToken:     c.refreshToken,
				ClaimInfo:        nil,
				CheckedAt:        timex.NowMonoSleep(),
			}
			dlog("failed checkin and no grace period, invalidating result")

			c.dispatchResult(result)
			return result, err
		}
	}

	return result, nil
}

func (c *DrmClient) doCheckinLockedRetry() (*drmtypes.Result, error) {
	var err error
	for i, delay := range retryDelays {
		if i > 0 {
			time.Sleep(delay)
		}

		var result *drmtypes.Result
		result, err = c.doCheckinLocked()
		if err == nil {
			return result, nil
		}
		if errors.Is(err, ErrVerify) {
			return nil, err
		}
		dlog("error ", i, " ", err)
	}

	return nil, err
}

func (c *DrmClient) doCheckinLocked() (*drmtypes.Result, error) {
	dlog("doCheckinLocked")
	resp, err := c.fetchNewEntitlement()
	if err != nil {
		return nil, err
	}

	// require strict version checking after the first checkin
	dlog("verify token")
	isFirstCheckin := c.LastResult() == nil
	claimInfo, err := c.verifier.Verify(resp.EntitlementToken, sjwt.TokenVerifyParams{
		StrictVersion: !isFirstCheckin,
	})
	if err != nil {
		dlog("verify failed: ", err)
		return nil, errors.Join(ErrVerify, err)
	}

	result := &drmtypes.Result{
		State:            drmtypes.StateValid,
		EntitlementToken: resp.EntitlementToken,
		RefreshToken:     resp.RefreshToken,
		ClaimInfo:        claimInfo,
		CheckedAt:/*wall*/ timex.NowMonoSleep(),
	}
	dlog("dispatch good result")

	c.dispatchResult(result)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshToken = resp.RefreshToken

	return result, nil
}

func (c *DrmClient) dispatchResult(result *drmtypes.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dlog("dispatchResult: ", result)
	c.lastResult = result
	c.setState(result.State)

	// report every period, to make sure scon stays alive
	go func() {
		err := c.reportToScon(result)
		if err != nil {
			logrus.WithError(err).Error("failed to report to scon")
		}
	}()
}

func (c *DrmClient) fetchNewEntitlement() (*drmtypes.EntitlementResponse, error) {
	req := &drmtypes.EntitlementRequest{
		RefreshToken: c.refreshToken,
		Identifiers:  *c.identifiers,
		AppVersion:   c.appVersion,
		ClientTime:   time.Now().UTC(),
	}
	dlog("POST req: ", req)

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Post(c.apiBaseURL+"/api/v0/drm/preview/entitlement", "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// was not a network error, so this is a good time to check update too
	go func() {
		err := c.updater.MaybeCheck()
		if err != nil {
			logrus.WithError(err).Error("failed to check update")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status code %d", resp.StatusCode)
	}

	var response drmtypes.EntitlementResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	dlog("response: ", response)
	return &response, nil
}

func Client() *DrmClient {
	return onceClient.Do(func() *DrmClient {
		client := newDrmClient()
		go client.Run()
		return client
	})
}

func dlog(msg string, args ...interface{}) {
	if verboseDebug {
		logrus.Debug(append([]interface{}{"[drm] " + msg}, args...)...)
	}
}
