package drm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
	"github.com/kdrag0n/macvirt/macvmgr/conf/appver"
	"github.com/kdrag0n/macvirt/macvmgr/drm/drmtypes"
	"github.com/kdrag0n/macvirt/macvmgr/drm/sjwt"
	"github.com/kdrag0n/macvirt/macvmgr/drm/timex"
	"github.com/kdrag0n/macvirt/macvmgr/vclient/iokit"
	"github.com/sirupsen/logrus"
)

const (
	// evaluate the local DRM state this often
	evaluateInterval = 15 * time.Minute
	// wait at least this long before sending another server checkin request
	// it should be 24h (1 day) but we use a shorter interval to allow for
	// faster recovery in case of network errors. it's 24h-30m (2*interval)
	checkinLifetime = 25 * time.Minute

	// allow non-explicit (i.e. network error) failures up to this long after startup
	startGracePeriod = 15 * time.Minute
	// wait this long for VM to stop if DRM failed
	FailStopTimeout = 2 * time.Minute

	// retry delays for DRM checkin requests
	retryDelay1 = 5 * time.Second
	retryDelay2 = 30 * time.Second
	retryDelay3 = 5 * time.Minute

	// temporary preview refresh token
	previewRefreshToken = "1181201e-23f8-41f6-9660-b7110f4bfedb"

	apiBaseUrlProd = "https://api-license.orbstack.dev"
	apiBaseUrlDev  = "http://localhost:8400"
)

var (
	verboseDebug = true
)

var (
	cachedClient   *DrmClient
	cachedClientMu sync.Mutex

	ErrVerify = errors.New("verification failed")
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

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    3,
			IdleConnTimeout: 60 * time.Second,
		},
	}

	baseURL := apiBaseUrlProd
	if conf.Debug() {
		baseURL = apiBaseUrlDev
	}

	return &DrmClient{
		// start in permissive valid state
		state:      drmtypes.StateValid,
		verifier:   sjwt.NewVerifier(ids, appVersion),
		http:       httpClient,
		apiBaseURL: baseURL,

		lastResult: nil,

		//TODO accounts
		refreshToken: previewRefreshToken,
		identifiers:  ids,
		appVersion:   appVersion,
		startTime:/*mono*/ timex.NowMonoSleep(),

		failChan: make(chan struct{}),
	}
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
	ticker := time.NewTicker(evaluateInterval)
	defer ticker.Stop()

	for ; true; <-ticker.C {
		dlog("periodic check")
		_, _ = c.KickCheck()
	}
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
		return nil, errors.New("asleep")
	}

	if !c.Valid() {
		dlog("skipping checkin due to invalid state")
		return nil, errors.New("invalid state")
	}

	result, err := c.doCheckinLockedRetry()
	if err != nil {
		isVerifyFail := errors.Is(err, ErrVerify)

		// new check failed. are we in grace period for old token expiry?
		if !isVerifyFail && lastResult != nil && /*wall*/ time.Now().Before(lastResult.ClaimInfo.ExpiresAt.Add(sjwt.NotAfterLeeway)) {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in last token grace period")
			return lastResult, nil
		} else if !isVerifyFail && lastResult == nil && /*mono*/ timex.SinceMonoSleep(c.startTime) < startGracePeriod {
			// still in grace period, so keep the old result
			dlog("failed checkin, but still in start grace period")
			return nil, err
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
	dlog("doCheckinLockedRetry 1")
	result, err := c.doCheckinLocked()
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrVerify) {
		return nil, err
	}
	dlog("1 error ", err)

	time.Sleep(retryDelay1)
	dlog("doCheckinLockedRetry 2")
	result, err = c.doCheckinLocked()
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrVerify) {
		return nil, err
	}
	dlog("2 error ", err)

	time.Sleep(retryDelay2)
	dlog("doCheckinLockedRetry 3")
	result, err = c.doCheckinLocked()
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrVerify) {
		return nil, err
	}
	dlog("3 error ", err)

	time.Sleep(retryDelay3)
	dlog("doCheckinLockedRetry 4")
	result, err = c.doCheckinLocked()
	if err == nil {
		return result, nil
	}
	if errors.Is(err, ErrVerify) {
		return nil, err
	}
	dlog("4 error ", err)

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

	return nil, nil
}

func (c *DrmClient) dispatchResult(result *drmtypes.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dlog("dispatchResult: ", result)
	c.lastResult = result
	c.setState(result.State)
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
	cachedClientMu.Lock()
	defer cachedClientMu.Unlock()

	if cachedClient != nil {
		return cachedClient
	}

	cachedClient = newDrmClient()
	go cachedClient.Run()

	return cachedClient
}

func dlog(msg string, args ...interface{}) {
	if verboseDebug {
		logrus.Info(append([]interface{}{"[drm] " + msg}, args...)...)
	}
}
