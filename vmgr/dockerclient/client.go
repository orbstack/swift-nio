package dockerclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const verboseDebug = false

type Client struct {
	dialer  func(ctx context.Context, network, addr string) (net.Conn, error)
	http    *http.Client
	proto   string
	addr    string
	baseURL string
}

type statusRecord struct {
	Error  string `json:"error"`
	Stream string `json:"stream"`
}

type Options struct {
	Unversioned bool
}

type APIError struct {
	Message    string
	ShowStatus bool
	HTTPStatus int
}

func (e *APIError) Error() string {
	if e.ShowStatus {
		return fmt.Sprintf("[Docker] %s (%d)", e.Message, e.HTTPStatus)
	} else {
		return fmt.Sprintf("[Docker] %s", e.Message)
	}
}

// https://github.com/moby/moby/blob/master/client/client.go#L403
func ParseHostURL(host string) (*url.URL, error) {
	proto, addr, ok := strings.Cut(host, "://")
	if !ok || addr == "" {
		return nil, fmt.Errorf("unable to parse docker host `%s`", host)
	}

	var basePath string
	if proto == "tcp" {
		parsed, err := url.Parse("tcp://" + addr)
		if err != nil {
			return nil, err
		}
		addr = parsed.Host
		basePath = parsed.Path
	}
	return &url.URL{
		Scheme: proto,
		Host:   addr,
		Path:   basePath,
	}, nil
}

// https://github.com/docker/cli/blob/dac7319f10d7cc22bc9e031dd930114e4b3d5111/cli/connhelper/connhelper.go#L25
func GetSSHDialer(dockerHost string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	sp, err := ParseSshURL(dockerHost)
	// disable pty allocation
	sshFlags := []string{"-T"}
	if err != nil {
		return nil, fmt.Errorf("ssh host connection is not valid")
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		args := []string{"docker"}
		if sp.Path != "" {
			args = append(args, "--host", "unix://"+sp.Path)
		}
		args = append(args, "system", "dial-stdio")
		return NewCommandConn(ctx, "ssh", append(sshFlags, sp.Args(args...)...)...)
	}, nil
}

func NewClient(dockerHost string) (*Client, error) {
	hostURL, err := ParseHostURL(dockerHost)
	if err != nil {
		return nil, err
	}

	var c *Client
	opts := &Options{Unversioned: true}

	switch hostURL.Scheme {
	case "ssh":
		dialer, err := GetSSHDialer(dockerHost)
		if err != nil {
			return nil, fmt.Errorf("could not connect to docker host via ssh")
		}
		c, err = NewWithDialer(dialer, opts)
		if err != nil {
			return nil, fmt.Errorf("could not connect to docker host via ssh")
		}
	case "unix":
		c, err = NewWithUnixSocket(hostURL.Path, opts)
		if err != nil {
			return nil, fmt.Errorf("could not connect to docker host via ssh")
		}
	default:
		return nil, fmt.Errorf("unsupported scheme %s", hostURL.Scheme)
	}

	c.proto = hostURL.Scheme
	c.addr = hostURL.Host
	return c, nil

}

func (c *Client) Dial(ctx context.Context) (net.Conn, error) {
	if c.dialer == nil {
		return nil, fmt.Errorf("client does not have a dialer")
	}
	return c.dialer(ctx, c.proto, c.addr)
}

func NewWithHTTP(dialer func(ctx context.Context, network, addr string) (net.Conn, error), httpC *http.Client, options *Options) *Client {
	baseURL := "http://docker/v1.43"
	if options != nil {
		if options.Unversioned {
			baseURL = "http://docker"
		}
	}

	return &Client{
		http:    httpC,
		dialer:  dialer,
		baseURL: baseURL,
	}
}

func NewWithDialer(dialer func(ctx context.Context, network, addr string) (net.Conn, error), options *Options) (*Client, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer,
			// idle conns ok usually
			MaxIdleConns:    3,
			IdleConnTimeout: 5 * time.Second,
			// TLSClientConfig: ,
		},
	}
	return NewWithHTTP(dialer, httpClient, options), nil
}

func NewWithUnixSocket(path string, options *Options) (*Client, error) {
	return NewWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", path)
	}, options)
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
		return &APIError{
			Message: string(errBody),
			// include HTTP status code if error isn't JSON
			ShowStatus: true,
			HTTPStatus: resp.StatusCode,
		}
	}

	return &APIError{
		Message:    jsonError.Message,
		ShowStatus: false,
		HTTPStatus: resp.StatusCode,
	}
}

func (c *Client) newRequest(method, path string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		// use it if it's already a reader
		if r, ok := body.(io.Reader); ok {
			reader = r
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("encode body: %w", err)
			}
			reader = bytes.NewReader(b)
		}
	}

	if verboseDebug {
		logrus.WithFields(logrus.Fields{
			"method": method,
			"path":   path,
			"body":   body,
		}).Debug("docker call")
	}

	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
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
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReadError(resp)
	}

	if out != nil {
		err = json.NewDecoder(resp.Body).Decode(out)
		if err != nil {
			return fmt.Errorf("decode resp: %w", err)
		}
	} else {
		// image pull doesn't work if we don't read the body
		err = ReadStream(resp.Body)
		if err != nil {
			return fmt.Errorf("read resp: %w", err)
		}
	}

	return nil
}

func (c *Client) CallRaw(method, path string, body any) (*http.Response, error) {
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	return resp, nil
}

func (c *Client) CallDiscard(method, path string, body any) error {
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ReadError(resp)
	}

	io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *Client) StreamHijack(method, path string, body any) (net.Conn, error) {
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return nil, err
	}

	ctx := req.Context()
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")

	conn, err := c.Dial(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not connect to docker daemon")
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	if err = req.Write(conn); err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("could not upgrade request")
	}
	return conn, nil
}

func (c *Client) StreamRead(method, path string, body any) (io.ReadCloser, error) {
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, ReadError(resp)
	}

	return resp.Body, nil
}

func (c *Client) StreamWrite(method, path string, body io.Reader) error {
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
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
