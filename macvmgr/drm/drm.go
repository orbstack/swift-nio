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
	"github.com/kdrag0n/macvirt/macvmgr/vclient/iokit"
)

const (
	checkinInterval  = 15 * time.Minute
	checkinLifetime  = 24 * time.Hour
	startGracePeriod = 15 * time.Minute
	FailStopTimeout  = 3 * time.Minute

	retryDelay1 = 5 * time.Second
	retryDelay2 = 30 * time.Second
	retryDelay3 = 5 * time.Minute

	previewRefreshToken = "1181201e-23f8-41f6-9660-b7110f4bfedb"
)

var (
	verboseDebug = conf.Debug()
)

var (
	cachedClient   *DrmClient
	cachedClientMu sync.Mutex
)

type DrmClient struct {
	mu       sync.Mutex
	checkMu  sync.Mutex
	verifier *sjwt.Verifier
	http     *http.Client

	state      drmtypes.State
	lastResult *drmtypes.Result

	refreshToken string
	identifiers  *drmtypes.Identifiers
	appVersion   drmtypes.AppVersion
	startTime    time.Time

	failChan chan struct{}
}

func newDrmClient() *DrmClient {
	ids, err := deriveIdentifiers()
	if err != nil {
		panic(err)
	}

	ver, err := appver.GitCommit()
	if err != nil {
		panic(err)
	}

	appVersion := drmtypes.AppVersion{
		Git: ver,
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:    3,
			IdleConnTimeout: 60 * time.Second,
		},
	}

	return &DrmClient{
		state:    drmtypes.StateInvalid,
		verifier: sjwt.NewVerifier(ids, appVersion),
		http:     httpClient,

		lastResult: nil,

		//TODO accounts
		refreshToken: previewRefreshToken,
		identifiers:  ids,
		appVersion:   appVersion,
		startTime:    time.Now(),

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
	if state == drmtypes.StateInvalid {
		c.failChan <- struct{}{}
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
	ticker := time.NewTicker(checkinInterval)
	defer ticker.Stop()

	go c.KickCheck()

	for range ticker.C {
		_, _ = c.KickCheck()
	}
}

func (c *DrmClient) KickCheck() (*drmtypes.Result, error) {
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	lastResult := c.LastResult()
	if lastResult != nil && lastResult.State == drmtypes.StateValid && time.Since(lastResult.CheckedAt) < checkinLifetime && time.Now().Before(lastResult.ClaimInfo.ExpiresAt.Add(sjwt.NotAfterLeeway)) {
		return lastResult, nil
	}

	if iokit.IsAsleep() {
		return nil, errors.New("asleep")
	}

	result, err := c.doCheckinLockedRetry()
	if err != nil {
		// new check failed. are we in grace period for old token expiry?
		if lastResult != nil && time.Now().Before(lastResult.ClaimInfo.ExpiresAt.Add(sjwt.NotAfterLeeway)) {
			// still in grace period, so keep the old result
			return lastResult, nil
		} else if lastResult == nil && time.Since(c.startTime) < startGracePeriod {
			// still in grace period, so keep the old result
			return nil, err
		} else {
			// no grace period, so invalidate the result
			result = &drmtypes.Result{
				State:            drmtypes.StateInvalid,
				EntitlementToken: "",
				RefreshToken:     c.refreshToken,
				ClaimInfo:        nil,
				CheckedAt:        time.Now(),
			}

			c.dispatchResult(result)
			return result, err
		}
	}

	return result, nil
}

func (c *DrmClient) doCheckinLockedRetry() (*drmtypes.Result, error) {
	result, err := c.doCheckinLocked()
	if err == nil {
		return result, nil
	}

	time.Sleep(retryDelay1)
	result, err = c.doCheckinLocked()
	if err == nil {
		return result, nil
	}

	time.Sleep(retryDelay2)
	result, err = c.doCheckinLocked()
	if err == nil {
		return result, nil
	}

	time.Sleep(retryDelay3)
	result, err = c.doCheckinLocked()
	if err == nil {
		return result, nil
	}

	return nil, err
}

func (c *DrmClient) doCheckinLocked() (*drmtypes.Result, error) {
	resp, err := c.fetchNewEntitlement()
	if err != nil {
		return nil, err
	}

	// require strict version checking after the first checkin
	isFirstCheckin := c.LastResult() == nil
	claimInfo, err := c.verifier.Verify(resp.EntitlementToken, sjwt.TokenVerifyParams{
		StrictVersion: !isFirstCheckin,
	})
	if err != nil {
		return nil, err
	}

	result := &drmtypes.Result{
		State:            drmtypes.StateValid,
		EntitlementToken: resp.EntitlementToken,
		RefreshToken:     resp.RefreshToken,
		ClaimInfo:        claimInfo,
		CheckedAt:        time.Now(),
	}

	c.dispatchResult(result)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshToken = resp.RefreshToken

	return nil, nil
}

func (c *DrmClient) dispatchResult(result *drmtypes.Result) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.lastResult = result
	c.setState(result.State)
}

func (c *DrmClient) fetchNewEntitlement() (*drmtypes.EntitlementResponse, error) {
	req := &drmtypes.EntitlementRequest{
		RefreshToken: c.refreshToken,
		Identifiers:  c.identifiers,
		AppVersion:   c.appVersion,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Post("https://localhost:8400/v1/drm/entitlement", "application/json", bytes.NewReader(reqBytes))
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
