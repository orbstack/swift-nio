package appapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	BaseURL = "https://api-license.orbstack.dev/api/v1"
	// this way we still use proxy
	// BaseURL = "http://0.0.0.0:8400/api/v1"
)

type Client struct {
	*http.Client

	longTimeoutClient *http.Client
}

func NewClient() *Client {
	transport := &http.Transport{
		MaxIdleConns:    3,
		IdleConnTimeout: 60 * time.Second,
		// TODO this may be the wrong proxy
		Proxy: func(req *http.Request) (*url.URL, error) {
			return http.ProxyFromEnvironment(req)
		},
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
	req, err := http.NewRequest("GET", BaseURL+endpoint, nil)
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
	req, err := http.NewRequest("POST", BaseURL+endpoint, reader)
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
	req, err := http.NewRequest("GET", BaseURL+endpoint, nil)
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
