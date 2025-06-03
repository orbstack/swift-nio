package dockerclient

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
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

	"github.com/orbstack/macvirt/vmgr/syncx"
	"github.com/sirupsen/logrus"
)

const verboseDebug = false

type Client struct {
	dialer               func(ctx context.Context, network, addr string) (net.Conn, error)
	http                 *http.Client
	proto                string
	addr                 string
	baseURL              string
	drmRegistryAuthToken string

	// establish a spare connection during client initialization to avoid the overhead
	// of creating the connection at the time of use. This is used for the exec stream hijack
	// for remote wormhole debug.
	//
	// spareConnChan is nil if there is no spare connection, and a channel if there is
	// a pending/created spare connection ready to be consumed
	spareConnChan chan net.Conn
	spareConnMu   syncx.Mutex
}

type statusRecord struct {
	Error  string `json:"error"`
	Status string `json:"status"`
	Stream string `json:"stream"`
}

type Options struct {
	Unversioned     bool
	CreateSpareConn bool
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

func NewClient(endpoint Endpoint, opts *Options) (*Client, error) {
	hostURL, err := url.Parse(endpoint.Host)
	if err != nil {
		return nil, err
	}

	var c *Client

	switch hostURL.Scheme {
	case "ssh":
		dialer, err := GetSSHDialer(endpoint.Host)
		if err != nil {
			return nil, fmt.Errorf("create ssh dialer: %w", err)
		}
		c, err = NewWithDialer(dialer, opts)
		if err != nil {
			return nil, fmt.Errorf("connect to ssh docker host: %w", err)
		}
	case "unix":
		c, err = NewWithUnixSocket(hostURL.Path, opts)
		if err != nil {
			return nil, fmt.Errorf("connect to unix docker host: %w", err)
		}
	case "tcp":
		c, err = NewWithTCP(hostURL.Host, endpoint, opts)
		if err != nil {
			return nil, fmt.Errorf("connect to tcp docker host: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported scheme %s", hostURL.Scheme)
	}
	c.proto = hostURL.Scheme
	c.addr = hostURL.Host

	if opts != nil && opts.CreateSpareConn {
		go c.createSpareConnection()
	}

	return c, nil
}

func NewClientWithDrmAuth(endpoint Endpoint, drmToken string, opts *Options) (*Client, error) {
	c, err := NewClient(endpoint, opts)
	if err != nil {
		return nil, err
	}
	authDetails := fmt.Sprintf(`{"username": "orbstack", "password": %q}`, drmToken)
	c.drmRegistryAuthToken = base64.URLEncoding.EncodeToString([]byte(authDetails))
	return c, nil
}

func (c *Client) DialContext(ctx context.Context) (net.Conn, error) {
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
		},
	}
	return NewWithHTTP(dialer, httpClient, options), nil
}

func NewWithTCP(address string, endpoint Endpoint, options *Options) (*Client, error) {
	var tlsConfig *tls.Config
	var err error
	var dialer func(ctx context.Context, _, _ string) (net.Conn, error)

	if endpoint.TLSData != nil {
		tlsConfig, err = endpoint.tlsConfig()
		if err != nil {
			return nil, err
		}

		dialer = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return tls.Dial("tcp", address, tlsConfig)
		}
	} else {
		tlsConfig = nil
		dialer = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return net.Dial("tcp", address)
		}
	}

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: dialer,
			// idle conns ok usually
			MaxIdleConns:    3,
			IdleConnTimeout: 5 * time.Second,
		},
	}
	return NewWithHTTP(dialer, httpClient, options), nil
}

func NewWithUnixSocket(path string, options *Options) (*Client, error) {
	return NewWithDialer(func(ctx context.Context, _, _ string) (net.Conn, error) {
		return net.Dial("unix", path)
	}, options)
}

func (c *Client) createSpareConnection() {
	c.spareConnMu.Lock()
	defer c.spareConnMu.Unlock()

	// if there's another spare connection (either pending or created), don't create a new one
	if c.spareConnChan != nil {
		return
	}

	c.spareConnChan = make(chan net.Conn, 1)
	conn, err := c.createKeepAliveConnection(context.Background())
	if err != nil {
		c.spareConnChan <- nil
	} else {
		c.spareConnChan <- conn
	}
}

func (c *Client) Close() error {
	c.http.CloseIdleConnections()

	c.spareConnMu.Lock()
	defer c.spareConnMu.Unlock()

	if c.spareConnChan != nil {
		// wait for channel and close spare connection if exists
		conn := <-c.spareConnChan
		if conn != nil {
			conn.Close()
		}
		close(c.spareConnChan)
	}

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
		if c.drmRegistryAuthToken != "" {
			req.Header.Set("X-Registry-Auth", c.drmRegistryAuthToken)
		} else {
			req.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString([]byte("{}")))
		}
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
		// must drain body to be able to reuse connection. json decode doesn't do that
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read resp: %w", err)
		}
		err = json.Unmarshal(body, out)
		if err != nil {
			return fmt.Errorf("decode resp: %w", err)
		}
	} else {
		// image pull doesn't work if we don't read the body
		err = ReadStream(resp.Body)
		io.Copy(io.Discard, resp.Body)
		if err != nil {
			return fmt.Errorf("read resp: %w", err)
		}
	}

	return nil
}

func (c *Client) CallStream(method, path string, body any, out io.Writer, isTerminal bool, terminalFd uintptr, pullingFromOverride *string) error {
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

	DisplayJSONMessagesStream(resp.Body, out, terminalFd, isTerminal, pullingFromOverride, nil)
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

func (c *Client) createKeepAliveConnection(ctx context.Context) (net.Conn, error) {
	conn, err := c.DialContext(ctx)
	if err != nil {
		return nil, err
	}

	if tcpConn, ok := conn.(*net.TCPConn); ok {
		_ = tcpConn.SetKeepAlive(true)
		_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
	}

	return conn, nil
}

func (c *Client) streamHijack(method, path string, body any) (io.Reader, io.WriteCloser, error) {
	req, err := c.newRequest(method, path, body)
	if err != nil {
		return nil, nil, err
	}

	ctx := req.Context()
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "tcp")

	var conn net.Conn

	c.spareConnMu.Lock()
	if c.spareConnChan != nil {
		conn = <-c.spareConnChan
		// reset channel to nil so that subsequent calls do not expect a spare connection
		c.spareConnChan = nil
	}
	c.spareConnMu.Unlock()

	if conn == nil {
		conn, err = c.createKeepAliveConnection(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("could not connect to docker endpoint: %w", err)
		}
	}

	err = req.Write(conn)
	if err != nil {
		return nil, nil, err
	}

	// return bufio reader back to caller so that any additional bytes of the stream
	// beyond the HTTP response are not lost if consumed in the first read
	readConn := bufio.NewReader(conn)
	resp, err := http.ReadResponse(readConn, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("do request: %w", err)
	}

	if resp.StatusCode != http.StatusSwitchingProtocols {
		defer resp.Body.Close()
		return nil, nil, ReadError(resp)
	}

	return readConn, conn, nil
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
