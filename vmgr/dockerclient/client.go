package dockerclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	http *http.Client
}

type statusRecord struct {
	Error  string `json:"error"`
	Stream string `json:"stream"`
}

func NewWithHTTP(httpC *http.Client) *Client {
	return &Client{
		http: httpC,
	}
}

func NewWithDialer(dialer func(ctx context.Context, network, addr string) (net.Conn, error)) (*Client, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer,
			// idle conns ok usually
			MaxIdleConns:    3,
			IdleConnTimeout: 5 * time.Second,
		},
	}
	return NewWithHTTP(httpClient), nil
}

func NewWithUnixSocket(path string) (*Client, error) {
	return NewWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", path)
	})
}

func NewWithTCP(addr string) (*Client, error) {
	return NewWithDialer(func(ctx context.Context, network, _ string) (net.Conn, error) {
		return net.Dial("tcp", addr)
	})
}

func (c *Client) Close() error {
	c.http.CloseIdleConnections()
	return nil
}

func ReadError(resp *http.Response) error {
	if resp.StatusCode == 304 { // Not Modified
		return nil
	}

	// read error message
	errBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read error body: %s (%s)", err, resp.Status)
	}
	var jsonError struct {
		Message string `json:"message"`
	}
	// try json
	err = json.Unmarshal(errBody, &jsonError)
	if err != nil {
		// fallback: plain text
		return fmt.Errorf("[Docker] %s (%s)", string(errBody), resp.Status)
	}

	return fmt.Errorf("[Docker] %s", jsonError.Message)
}

func newRequest(method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		// use it if it's already a reader
		if r, ok := body.(io.Reader); ok {
			reader = r
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("encode body: %s", err)
			}
			reader = bytes.NewReader(b)
		}
	}

	/*
		logrus.WithFields(logrus.Fields{
			"method": method,
			"path":   path,
			"body":   body,
		}).Debug("docker call")
	*/

	req, err := http.NewRequest(method, "http://docker/v1.43"+path, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %s", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.HasPrefix(path, "/images/") {
		req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString([]byte("{}")))
	}

	return req, nil
}

func (c *Client) Call(method, path string, body any, out any) error {
	req, err := newRequest(method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReadError(resp)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %s", err)
		}
	} else {
		// image pull doesn't work if we don't read the body
		err = ReadStream(resp.Body)
		if err != nil {
			return fmt.Errorf("read resp: %s", err)
		}
	}

	return nil
}

func (c *Client) CallDiscard(method, path string, body any) error {
	req, err := newRequest(method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReadError(resp)
	}

	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *Client) StreamRead(method, path string, body any) (io.ReadCloser, error) {
	req, err := newRequest(method, path, body)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %s", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, ReadError(resp)
	}

	return resp.Body, nil
}

func (c *Client) StreamWrite(method, path string, body io.Reader) error {
	req, err := newRequest(method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReadError(resp)
	}

	// read body
	return ReadStream(resp.Body)
}

func ReadStream(body io.Reader) error {
	for {
		var record statusRecord
		err := json.NewDecoder(body).Decode(&record)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			// failed to decode = probably not error record
			// eg. images-rm array
			return nil
		}

		if record.Error != "" {
			return fmt.Errorf("(remote) %s", record.Error)
		}
	}
}
