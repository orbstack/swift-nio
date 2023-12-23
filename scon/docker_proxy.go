package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"slices"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// must have constant size for bufio, but we use EWMA for copying
const dockerProxyBufferSize = 65536

// sentinel to exit nested func
var errCloseConn = errors.New("close conn")

type DockerProxy struct {
	container *Container
	manager   *ConManager
	l         net.Listener
}

func (m *ConManager) startDockerProxy() error {
	l, err := netx.ListenTCP("tcp", &net.TCPAddr{
		// NIC interface, port 2375
		IP:   util.DefaultAddress4(),
		Port: ports.GuestDocker,
	})
	if err != nil {
		return err
	}

	c, err := m.GetByID(ContainerIDDocker)
	if err != nil {
		return err
	}

	proxy := &DockerProxy{
		manager:   m,
		container: c,
		l:         l,
	}
	m.dockerProxy = proxy

	go runOne("Docker proxy", proxy.Run)
	return nil
}

// lower-level, high-performance HTTP/1.1 proxy
// does raw TCP copies whenever possible
// preserves 1-to-1 connection mapping for simplicity
//   - less conn reuse - worse for perf, but much simpler
//   - but there's no reason this can't be implemented with a conn pool + TTL
//
// handles all cases of body copying: TCP upgrades, half duplex, chunked TE, content-length, Connection=close
// copies much faster than httputil.ReverseProxy
func (p *DockerProxy) serveConn(clientConn net.Conn) (retErr error) {
	defer clientConn.Close()

	inRequest := false
	defer func() {
		// if we're in a request but NOT a response body, then it's OK to send the error to the client
		if retErr != nil && inRequest {
			// send 502 error with body
			resp := &http.Response{
				StatusCode: http.StatusBadGateway,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(retErr.Error())),
			}
			resp.Header.Set("Content-Type", "text/plain")
			resp.Header.Set("Connection", "close")
			_ = resp.Write(clientConn)
		}
	}()

	// start docker container if not running
	if !p.container.Running() {
		logrus.Debug("docker not running, starting")
		err := p.container.Start()
		if err != nil {
			return fmt.Errorf("start docker: %w", err)
		}
	}

	// open upstream connection
	upstreamConn, err := UseAgentRet(p.container, func(a *agent.Client) (net.Conn, error) {
		return a.DockerDialSocket()
	})
	if err != nil {
		return fmt.Errorf("dial upstream: %w", err)
	}
	defer upstreamConn.Close()

	// default buffer size is 4096!
	// unfortunately it's very hard to make this adaptive, and we don't want too many slow 1-byte reads to avoid buffering
	clientBufReader := bufio.NewReaderSize(clientConn, dockerProxyBufferSize)
	for {
		// read request
		logrus.Trace("hp: reading request")
		req, err := http.ReadRequest(clientBufReader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read request: %w", err)
		}
		inRequest = true

		// restore Host (deleted by ReadRequest)
		req.Header.Set("Host", req.Host)

		// if we're starting a container, we need to synchronize container change events for NFS unmount
		// this prevents overlayfs upperdir/workdir concurrent reuse race
		// TODO check full path. this skips /containers/ check because of optional API version prefix
		if strings.HasSuffix(req.URL.Path, "/start") {
			logrus.Debug("synchronizing events for container start")
			err := p.container.UseAgent(func(a *agent.Client) error {
				return a.DockerSyncEvents()
			})
			if err != nil {
				logrus.WithError(err).Error("failed to synchronize events for container start")
			}
		}

		err = func() error {
			// take freezer ref on a per-request level
			// fixes idle conns from user's tools keeping machine alive
			// only possible to do it race-free from scon side
			freezer := p.container.Freezer()
			if freezer == nil {
				return fmt.Errorf("docker freezer is nil")
			}
			freezer.IncRef()
			defer freezer.DecRef()

			// send request
			logrus.Trace("hp: writing request")
			err = req.Write(upstreamConn)
			if err != nil {
				return fmt.Errorf("write request: %w", err)
			}

			// read response
			logrus.Trace("hp: reading response")
			upstreamBufReader := bufio.NewReaderSize(upstreamConn, dockerProxyBufferSize)
			resp, err := http.ReadResponse(upstreamBufReader, req)
			if err != nil {
				return fmt.Errorf("read response: %w", err)
			}

			// restore Content-Length and Transfer-Encoding (deleted by ReadResponse)
			// must not send Content-Length=0 for 101 UPGRADED, or JetBrains client breaks
			if resp.ContentLength != -1 && resp.StatusCode != http.StatusSwitchingProtocols {
				logrus.Tracef("hp: restoring Content-Length %d", resp.ContentLength)
				resp.Header.Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
			}
			if len(resp.TransferEncoding) > 0 {
				logrus.Tracef("hp: restoring Transfer-Encoding %s", strings.Join(resp.TransferEncoding, ", "))
				resp.Header.Set("Transfer-Encoding", strings.Join(resp.TransferEncoding, ", "))
			}

			// prep for proxying: if not 101, no Content-Length, and no Transfer-Encoding=chunked, then set Connection=close
			if resp.StatusCode != http.StatusSwitchingProtocols && resp.ContentLength == -1 && !slices.Contains(resp.TransferEncoding, "chunked") {
				resp.Header.Set("Connection", "close")
			}

			if resp.Trailer != nil {
				return fmt.Errorf("trailer not supported")
			}

			// send response *without* body, but with Content-Length and Transfer-Encoding
			// Response.Write excludes those headers, so roll our own
			// complicated status text is to preserve original status line w/o duplicate status code
			logrus.Trace("hp: writing response")
			_, err = fmt.Fprintf(clientConn, "HTTP/%d.%d %03d %s\r\n", resp.ProtoMajor, resp.ProtoMinor, resp.StatusCode, strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode)+" "))
			if err != nil {
				return fmt.Errorf("write response: %w", err)
			}
			inRequest = false // we've started responding, so can't send an error response until next req
			err = resp.Header.Write(clientConn)
			if err != nil {
				return fmt.Errorf("write response header: %w", err)
			}
			// end of response
			_, err = io.WriteString(clientConn, "\r\n")
			if err != nil {
				return fmt.Errorf("write response end: %w", err)
			}

			// proxy response body
			// this is the complicated part.
			// to allow connection reuse:
			// - if switching protocols (101), copy BOTH directions until EOF, and close conns (fast)
			// - if we have a Content-Length, copy that many bytes, and loop for next request (SLOW-ish b/c LimitedReader blocks splice)
			// - if Transfer-Encoding = chunked, copy by chunks. (SLOW!!)
			// - else, copy until EOF, and close conns (fast)
			if resp.StatusCode == http.StatusSwitchingProtocols {
				// flush remaining bufio data
				// ignore errors in case one side is already closed for write
				logrus.Trace("hp: flushing")
				var flushBuf [dockerProxyBufferSize]byte
				n, err := clientBufReader.Read(flushBuf[:clientBufReader.Buffered()])
				if err == nil {
					// flush (write) to upstream
					_, _ = upstreamConn.Write(flushBuf[:n])
				}
				n, err = upstreamBufReader.Read(flushBuf[:upstreamBufReader.Buffered()])
				if err == nil {
					// flush (write) to client
					_, _ = clientConn.Write(flushBuf[:n])
				}

				// copy the rest in both directions, then close conns
				logrus.Trace("hp: copying 101")
				tcppump.Pump2SpTcpUnix(clientConn.(*net.TCPConn), upstreamConn.(*net.UnixConn))
				return errCloseConn
			} else if resp.Body != nil && req.Method != http.MethodHead {
				// must NOT copy body for HEAD requests
				// it still sends same Content-Length response, but no body, so proxy hangs
				logrus.Trace("hp: copying body")
				closeConn, err := copyBody(resp.ContentLength, resp.TransferEncoding, clientConn, upstreamBufReader)
				if err != nil {
					return fmt.Errorf("copy body: %w", err)
				}
				// Connection=close case
				if closeConn {
					return errCloseConn
				}
			}

			return nil
		}()
		if err != nil {
			if err == errCloseConn {
				return nil
			} else {
				return fmt.Errorf("handle request: %w", err)
			}
		}
	}
}

// returns: (closeConn, err)
func copyBody(contentLength int64, transferEncoding []string, dst io.Writer, src io.Reader) (bool, error) {
	// this does NOT cover 101 case, as that's only for response
	if slices.Contains(transferEncoding, "chunked") {
		// this is the tricky, and slow, part
		// need chunked reader and writer, plus bufio part
		logrus.Trace("hp: copyBody: copying TE chunked body")
		chunkedReader := httputil.NewChunkedReader(src)
		chunkedWriter := httputil.NewChunkedWriter(dst)
		_, err := tcppump.CopyBuffer(chunkedWriter, chunkedReader, nil)
		if err != nil {
			return false, fmt.Errorf("copy TE: %w", err)
		}

		// write final empty chunk and trailer+crlf
		chunkedWriter.Close()
		_, err = io.WriteString(dst, "\r\n")
		if err != nil {
			return false, fmt.Errorf("write trailer: %w", err)
		}

		// keep conn open for reuse
		return false, nil
	} else if contentLength != -1 {
		// TODO: can we use splice here?
		logrus.Trace("hp: copyBody: copying CL body")
		limitReader := io.LimitReader(src, contentLength)
		_, err := tcppump.CopyBuffer(dst, limitReader, nil)
		if err != nil {
			return false, fmt.Errorf("copy CL: %w", err)
		}
		// keep conn open for reuse
		return false, nil
	} else {
		// single-direction upstream->client copy, then close conns
		// we already set Connection=close
		logrus.Trace("hp: copyBody: copying body until EOF")
		_, err := tcppump.CopyBuffer(dst, src, nil)
		if err != nil {
			return false, fmt.Errorf("copy CC: %w", err)
		}
		// *CLOSE* conn. impossible to know when it's safe to reuse until FIN
		return true, nil
	}
}

func (p *DockerProxy) kickStart(freezer *Freezer) {
	logrus.Debug("waiting for docker start")
	// this fails if agent socket is closed
	err := p.container.UseAgent(func(a *agent.Client) error {
		return a.DockerWaitStart()
	})
	if err != nil {
		logrus.WithError(err).Error("failed to wait for docker start")
		return
	}

	logrus.Debug("docker started, dropping freezer ref")
	freezer.DecRef()
}

func (p *DockerProxy) Run() error {
	for {
		clientConn, err := p.l.Accept()
		if err != nil {
			return err
		}

		go func() {
			err := p.serveConn(clientConn)
			// no point in logging broken pipe. that's "normal"
			if err != nil && !errors.Is(err, unix.EPIPE) {
				logrus.WithError(err).Error("docker conn failed")
			}
		}()
	}
}

func (p *DockerProxy) Close() error {
	return p.l.Close()
}
