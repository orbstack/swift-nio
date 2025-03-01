package appapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/orbstack/macvirt/vmgr/swext"
	"github.com/orbstack/macvirt/vmgr/vmconfig"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/orbstack/macvirt/scon/conf"
)

const (
	baseURLRelease = "https://api-license.orbstack.dev/api/v1"
	// this way we still use proxy. http.ProxyFromEnvironment ignores localhost
	baseURLDebug = "http://0.0.0.0:8400/api/v1"
)

func baseURL() string {
	if conf.Debug() && os.Getenv("ORB_DRM_DEBUG") == "1" {
		return baseURLDebug
	} else {
		return baseURLRelease
	}
}

type Client struct {
	*http.Client

	longTimeoutClient *http.Client
}

func getProxyForRequest(req *http.Request) (*url.URL, error) {
	// check env before checking macos proxy
	envProxy, err := http.ProxyFromEnvironment(req)
	if err != nil || envProxy != nil {
		return envProxy, err
	}

	// doesn't update from vmgr, but this is only used for short-lived commands, so it *should* be fine?
	configVal := vmconfig.Get().NetworkProxy

	needAuth := configVal == vmconfig.ProxyAuto
	settings, err := swext.ProxyGetSettings(needAuth)
	if err != nil {
		return nil, err
	}

	// if proxy is set in vmconfig, use it
	// otherwise, use (in order of precedence) system socks5, {https, http} proxy
	// some code taken from vmgr/vnet/tcpfwd/proxy.go:updateDialers

	switch configVal {
	case vmconfig.ProxyAuto:
		break
	case vmconfig.ProxyNone:
		return nil, nil
	default:
		u, err := url.Parse(configVal)
		if err != nil {
			return nil, err
		}

		// normalize socks5h -> socks5
		if u.Scheme == "socks5h" {
			u.Scheme = "socks5"
		}

		return u, nil
	}

	if settings.SOCKSEnable {
		u := &url.URL{
			Scheme: "socks5",
			Host:   net.JoinHostPort(settings.SOCKSProxy, strconv.Itoa(settings.SOCKSPort)),
		}
		if settings.SOCKSUser != "" {
			u.User = url.UserPassword(settings.SOCKSUser, settings.SOCKSPassword)
		}

		return u, nil
	}

	if settings.HTTPSEnable && req.TLS != nil {
		u := &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(settings.HTTPSProxy, strconv.Itoa(settings.HTTPSPort)),
		}

		if settings.HTTPSUser != "" {
			u.User = url.UserPassword(settings.HTTPSUser, settings.HTTPSPassword)
		}

		return u, nil
	}

	if settings.HTTPEnable && req.TLS == nil {
		u := &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(settings.HTTPProxy, strconv.Itoa(settings.HTTPPort)),
		}

		if settings.HTTPUser != "" {
			u.User = url.UserPassword(settings.HTTPUser, settings.HTTPPassword)
		}

		return u, nil
	}

	return nil, nil
}

func NewClient() *Client {
	transport := &http.Transport{
		MaxIdleConns:    3,
		IdleConnTimeout: 60 * time.Second,
		Proxy:           getProxyForRequest,
	}
	httpClient := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}

	return &Client{
		Client: httpClient,
		longTimeoutClient: &http.Client{
			Timeout:   5 * time.Minute,
			Transport: transport,
		},
	}
}

func (c *Client) Get(endpoint string) (*http.Response, error) {
	req, err := http.NewRequest("GET", baseURL()+endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return resp, nil
}

func (c *Client) Post(endpoint string, body any, out any) error {
	msg, err := json.Marshal(body)
	if err != nil {
		return err
	}

	reader := bytes.NewReader(msg)
	req, err := http.NewRequest("POST", baseURL()+endpoint, reader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %w", err)
		}
	} else {
		io.Copy(io.Discard, resp.Body)
	}

	return nil
}

func readError(resp *http.Response) error {
	// read error message
	errBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error body: %s (%s)", err, resp.Status)
	}

	return fmt.Errorf("[api] %s (%s)", string(errBody), resp.Status)
}

func (c *Client) LongGet(endpoint string, out any) error {
	req, err := http.NewRequest("GET", baseURL()+endpoint, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	resp, err := c.longTimeoutClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return readError(resp)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %w", err)
		}
	} else {
		io.Copy(io.Discard, resp.Body)
	}

	return nil
}
