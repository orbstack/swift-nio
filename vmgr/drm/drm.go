package drm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/orbstack/macvirt/scon/isclient"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appver"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"github.com/orbstack/macvirt/vmgr/drm/sjwt"
	"github.com/orbstack/macvirt/vmgr/drm/timex"
	"github.com/orbstack/macvirt/vmgr/drm/updates"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/uitypes"
	"github.com/orbstack/macvirt/vmgr/vclient/iokit"
	"github.com/orbstack/macvirt/vmgr/vnet"
	"github.com/orbstack/macvirt/vmgr/vzf"
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

	// allow this many failures before shutting down
	// first fail is to send a GUI warning, second fail shuts down
	failLimit = 2

	// after VM wakes up from sleep, wait this long for time/clock sync before reporting
	// this ensures host and VM clock are synced again
	// this is 4 burst reqs * 2 secs interval * 2x syncs + margin = 30 secs
	sleepSyncPeriod = 30 * time.Second

	apiBaseUrlProd = "https://api-license.orbstack.dev"
	apiBaseUrlDev  = "http://localhost:8400"
)

var (
	verboseDebug = conf.Debug()

	// retry delays for DRM checkin requests
	retryDelays = []time.Duration{0, 5 * time.Second, 30 * time.Second, 5 * time.Minute}
)

var (
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

	persistResultCh     chan *drmtypes.Result
	failChan            chan struct{}
	failCountSinceValid int
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
	if conf.Debug() && os.Getenv("ORB_DRM_DEBUG") == "1" {
		baseURL = apiBaseUrlDev
	}

	c := &DrmClient{
		// start in permissive valid state
		state:    drmtypes.StateValid,
		verifier: sjwt.NewVerifier(ids, appVersion),
		/*http below*/
		apiBaseURL: baseURL,

		lastResult: nil,

		identifiers: ids,
		appVersion:  appVersion,
		startTime:/*mono*/ timex.NowMonoSleep(),

		updater: updates.NewUpdater(),

		persistResultCh: make(chan *drmtypes.Result, 1),
		failChan:        make(chan struct{}),
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
		// close OK: signal select loop
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

	go c.runStatePersister()
	go func() {
		err := c.restoreState()
		if err != nil {
			dlog("restore state failed: ", err)
		}
	}()

	// this includes a first iteration
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

func (c *DrmClient) restoreState() error {
	dlog("restoreState")
	data, err := drmcore.ReadKeychainState()
	if err != nil {
		return err
	}
	if len(data) == 0 {
		dlog("restore: no data")
		return nil
	}

	var state drmtypes.PersistentState
	err = json.Unmarshal(data, &state)
	if err != nil {
		return err
	}
	//TODO encrypt by device id - or pointless b/c weak/honor DRM anyway

	// use non-strict version check for restore
	dlog("restore: verify token")
	claimInfo, err := c.verifier.Verify(state.EntitlementToken, sjwt.TokenVerifyParams{
		StrictVersion: false,
	})

	c.mu.Lock() // take it here for setting refresh token
	defer c.mu.Unlock()

	// always salvage the refresh token
	if c.refreshToken == "" {
		dlog("restore: use refresh token: ", state.RefreshToken)
		c.refreshToken = state.RefreshToken
	}

	if err != nil {
		// never dispatch invalid results here, just get it from the server again
		dlog("restore: verify failed: ", err)
		return errors.Join(ErrVerify, err)
	}

	// we're under lock now.
	// this is normally the first result that gets dispatched, so if there's a new one from the server, don't overwrite it with an old one
	if c.lastResult != nil {
		dlog("restore: already got new result from server, skip")
		return nil
	}

	dlog("restore: dispatch good result")
	result := &drmtypes.Result{
		State:            drmtypes.StateValid,
		EntitlementToken: state.EntitlementToken,
		RefreshToken:     state.RefreshToken,
		ClaimInfo:        claimInfo,
		// needed to prevent re-check from being deferred for too long
		CheckedAt:/*wall*/ state.FetchedAt,
	}
	c.dispatchResultLocked(result)
	c.refreshToken = state.RefreshToken
	return nil
}

func (c *DrmClient) persistState(result *drmtypes.Result) error {
	// only persist valid states, no point in saving invalid if this is for optimistic cache on start
	if result.State != drmtypes.StateValid {
		dlog("persistState: skip invalid state")
		return nil
	}

	dlog("persistState")
	//TODO encrypt by device id
	state := drmtypes.PersistentState{
		RefreshToken:     result.RefreshToken,
		EntitlementToken: result.EntitlementToken,
		FetchedAt:        result.CheckedAt,
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}

	err = drmcore.SetKeychainState(data)
	if err != nil {
		return err
	}

	return nil
}

// keychain can hang on permission prompt, so do it in a goroutine
func (c *DrmClient) runStatePersister() {
	for result := range c.persistResultCh {
		err := c.persistState(result)
		if err != nil {
			dlog("bg: persist state failed: ", err)
		}
	}
}

func (c *DrmClient) sendGUIWarning(lastError error) {
	// post notification
	err := guihelper.Notify(guitypes.Notification{
		Title:   "OrbStack will stop working soon",
		Message: "Can’t verify license. Please restore network access or save your work.",
		Silent:  false,
	})
	if err != nil {
		logrus.WithError(err).Error("failed to post notification")
	}

	// send alert to GUI if window is open
	vzf.SwextIpcNotifyUIEvent(uitypes.UIEvent{
		DrmWarning: &uitypes.DrmWarningEvent{
			LastError: lastError.Error(),
		},
	})
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
		dlog("waiting for sleep-wake time sync")
		time.Sleep(sleepSyncPeriod)
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
		conn, err := c.vnet.DialGuestTCPRetry(context.TODO(), ports.GuestSconRPCInternal)
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
		// a verification failure after fetch is always an immediate fail.
		if errors.Is(err, ErrVerify) {
			dlog("invalidating result: explicit verification failure")
			c.dispatchFail()
			return result, err
		}

		// new check failed. are we in grace period for old token expiry?
		wakeTime := iokit.LastWakeTime
		if lastResult != nil && /*wall*/ time.Now().Before(lastResult.ClaimInfo.ExpiresAt.Add(sjwt.NotAfterLeeway)) {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in last token grace period")
			return lastResult, nil
		} else if lastResult == nil && /*mono*/ timex.SinceMonoSleep(c.startTime) < startGracePeriod {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in start grace period")
			return nil, errors.Join(err, ErrGrace)
		} else if lastResult == nil && wakeTime != nil && /*mono*/ timex.SinceMonoSleep(*wakeTime) < startGracePeriod {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in wake grace period")
			return nil, errors.Join(err, ErrGrace)
		} else if c.failCountSinceValid < failLimit {
			// not in any grace period, but this is the first time we've failed after the last successful check
			// on first failure, set a flag and send a warning.
			dlog("failed checkin, but this is first fail - sending warning")
			c.failCountSinceValid++
			c.sendGUIWarning(err)
			// propagate log error
			return result, err
		} else {
			// not in any grace period, and we've failed twice since the last successful check
			// on second failure, shut down
			dlog("invalidating result: failed checkin, no grace period, already sent warning (2nd fail)")
			c.failCountSinceValid++
			c.dispatchFail()
			return result, err
		}
	}

	// success = reset fail counter
	c.failCountSinceValid = 0
	return result, nil
}

func (c *DrmClient) dispatchFail() {
	result := &drmtypes.Result{
		State:            drmtypes.StateInvalid,
		EntitlementToken: "",
		RefreshToken:     c.refreshToken,
		ClaimInfo:        nil,
		CheckedAt:        timex.NowMonoSleep(),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.dispatchResultLocked(result)
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

	dlog("dispatch good result")
	result := &drmtypes.Result{
		State:            drmtypes.StateValid,
		EntitlementToken: resp.EntitlementToken,
		RefreshToken:     resp.RefreshToken,
		ClaimInfo:        claimInfo,
		CheckedAt:/*wall*/ timex.NowMonoSleep(),
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.dispatchResultLocked(result)
	c.refreshToken = resp.RefreshToken
	return result, nil
}

func (c *DrmClient) dispatchResultLocked(result *drmtypes.Result) {
	dlog("dispatchResult: ", result)
	c.lastResult = result
	c.setState(result.State)
	// don't block if perm prompt is stuck
	select {
	case c.persistResultCh <- result:
	default:
	}

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

	resp, err := c.http.Post(c.apiBaseURL+"/api/v1/drm/entitlement", "application/json", bytes.NewReader(reqBytes))
	if err != nil {
		// hide the path part of the URL
		return nil, errors.New(strings.Replace(err.Error(), "/api/v1/drm/entitlement", "/...", 1))
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

var Client = sync.OnceValue(func() *DrmClient {
	client := newDrmClient()
	go client.Run()
	return client
})

func dlog(msg string, args ...interface{}) {
	if verboseDebug {
		logrus.Debug(append([]interface{}{"[drm] " + msg}, args...)...)
	}
}
