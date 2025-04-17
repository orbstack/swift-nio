package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/tcpfwd/tcppump"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// must have constant size for bufio, but we use EWMA for copying
const dockerProxyBufferSize = 65536

// JSON request body size limit to prevent memory exhaustion
const dockerProxyRequestBodyLimit = 15 * 1024 * 1024 // 15 MiB

// sentinel to exit nested func
var errCloseConn = errors.New("close conn")

type DockerProxy struct {
	container    *Container
	manager      *ConManager
	tcpListener  net.Listener
	unixListener net.Listener
}

func (m *ConManager) startDockerProxy() error {
	tcpListener, err := netx.ListenTCP("tcp", &net.TCPAddr{
		// NIC interface, port 2375
		IP:   vnetGuestIP4,
		Port: ports.GuestDocker,
	})
	if err != nil {
		return err
	}

	_ = os.Remove(mounts.HostDockerSocket)
	// listen with 0660 to fix perms for users bind mounting into containers.
	// 2375 is the gid of the docker group in docker:dind (it's also the port of docker lol) but people seem to expect gid 0 so that's what we'll use
	unixListener, err := util.ListenUnixWithPerms(mounts.HostDockerSocket, 0660, 0, 0)
	if err != nil {
		return err
	}

	c, err := m.GetByID(ContainerIDDocker)
	if err != nil {
		return err
	}

	proxy := &DockerProxy{
		manager:      m,
		container:    c,
		tcpListener:  tcpListener,
		unixListener: unixListener,
	}
	m.dockerProxy = proxy

	go runOne("Docker proxy (TCP)", proxy.RunTCP)
	go runOne("Docker proxy (unix)", proxy.RunUnix)
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
				ProtoMajor: 1,
				ProtoMinor: 1,
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
		return a.DockerDialRealSocket()
	})
	if err != nil {
		return fmt.Errorf("dial upstream: %w", err)
	}
	defer upstreamConn.Close()

	// default buffer size is 4096!
	// unfortunately it's very hard to make this adaptive, and we don't want too many slow 1-byte reads to avoid buffering
	clientBufReader := bufio.NewReaderSize(clientConn, dockerProxyBufferSize)
	upstreamBufReader := bufio.NewReaderSize(upstreamConn, dockerProxyBufferSize)
	for {
		state := &RequestState{}

		// read request
		logrus.Trace("hp: reading request")
		req, err := http.ReadRequest(clientBufReader)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, unix.ECONNRESET) {
				return nil
			} else {
				return fmt.Errorf("read request: %w", err)
			}
		}
		inRequest = true

		// restore Host (deleted by ReadRequest)
		req.Header.Set("Host", req.Host)

		err = func() error {
			// close req body even if we fail before writing the request out
			// defer is resolved at call time, so this also works after filterRequest modifies the body
			defer req.Body.Close()

			// take freezer ref on a per-request level
			// fixes idle conns from user's tools keeping machine alive
			// only possible to do it race-free from scon side
			freezer := p.container.Freezer()
			if freezer == nil {
				return fmt.Errorf("docker freezer is nil")
			}
			freezer.IncRef()
			defer freezer.DecRef()

			err := p.filterRequest(req, state)
			if err != nil {
				return fmt.Errorf("filter request: %w", err)
			}

			// send request
			logrus.Trace("hp: writing request ", req.Method, req.URL)
			err = req.Write(upstreamConn)
			// why doesn't errors.Is(err, unix.EPIPE) work here?
			if err != nil && !strings.Contains(err.Error(), "write: broken pipe") {
				// can still attempt to read a response if we got cut off.
				return fmt.Errorf("write request: %w", err)
			}

			// read response
			logrus.Trace("hp: reading response")
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
			// if client wants to close the conn (but server didn't request it), also set Connection=close
			// "close" is the only valid value: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Connection
			// no value = implicit keep-alive
			if req.Header.Get("Connection") == "close" {
				resp.Header.Set("Connection", "close")
			}

			if resp.Trailer != nil {
				return fmt.Errorf("trailer not supported")
			}

			// send response *without* body, but with Content-Length and Transfer-Encoding
			// Response.Write excludes those headers, so roll our own
			// complicated status text is to preserve original status line w/o duplicate status code
			logrus.Trace("hp: writing response ", resp.Status)
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
				if tcpConn, ok := clientConn.(*net.TCPConn); ok {
					tcppump.Pump2SpTcpUnix(tcpConn, upstreamConn.(*net.UnixConn))
				} else if unixConn, ok := clientConn.(*net.UnixConn); ok {
					tcppump.Pump2SpUnixUnix(unixConn, upstreamConn.(*net.UnixConn))
				} else {
					return errors.New("unsupported conn type")
				}
				return errCloseConn
			} else if resp.Body != nil && req.Method != http.MethodHead {
				// must NOT copy body for HEAD requests
				// it still sends same Content-Length response, but no body, so proxy hangs
				logrus.Trace("hp: copying body")
				closeConn, err := p.copyBody(resp, clientConn, upstreamBufReader, state)
				if err != nil {
					return fmt.Errorf("copy body: %w", err)
				}
				// Connection=close case
				if closeConn {
					return errCloseConn
				}
			}

			// if we said we're closing the conn (either by upstream or by client request), then do it
			if resp.Header.Get("Connection") == "close" {
				return errCloseConn
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
func (p *DockerProxy) copyBody(resp *http.Response, dst io.Writer, src io.Reader, state *RequestState) (bool, error) {
	// this does NOT cover 101 case, as that's only for response
	if slices.Contains(resp.TransferEncoding, "chunked") {
		// this is the tricky, and slow, part
		// need chunked reader and writer, plus bufio part
		logrus.Trace("hp: copyBody: copying TE chunked body")
		chunkedReader := httputil.NewChunkedReader(src)
		chunkedWriter := httputil.NewChunkedWriter(dst)
		err := p.filterResponseBody(resp, chunkedWriter, chunkedReader, state)
		if err != nil {
			return false, fmt.Errorf("copy TE: %w", err)
		}

		// read trailer crlf
		var trailer [2]byte
		_, err = io.ReadFull(src, trailer[:])
		if err != nil {
			return false, fmt.Errorf("read trailer: %w", err)
		}
		if trailer[0] != '\r' || trailer[1] != '\n' {
			return false, fmt.Errorf("invalid trailer")
		}

		// write final empty chunk and trailer+crlf
		chunkedWriter.Close()
		_, err = io.WriteString(dst, "\r\n")
		if err != nil {
			return false, fmt.Errorf("write trailer: %w", err)
		}

		// keep conn open for reuse
		return false, nil
	} else if resp.ContentLength != -1 {
		// TODO: can we use splice here?
		logrus.Trace("hp: copyBody: copying CL body")
		limitReader := io.LimitReader(src, resp.ContentLength)
		err := p.filterResponseBody(resp, dst, limitReader, state)
		if err != nil {
			return false, fmt.Errorf("copy CL: %w", err)
		}
		// keep conn open for reuse
		return false, nil
	} else {
		// single-direction upstream->client copy, then close conns
		// we already set Connection=close
		logrus.Trace("hp: copyBody: copying body until EOF")
		err := p.filterResponseBody(resp, dst, src, state)
		if err != nil {
			return false, fmt.Errorf("copy CC: %w", err)
		}
		// *CLOSE* conn. impossible to know when it's safe to reuse until FIN
		return true, nil
	}
}

type RequestState struct {
}

func (p *DockerProxy) filterRequest(req *http.Request, state *RequestState) error {
	// account for versioned and unversioned API paths
	// TODO: does this normalize double slashes?
	// TODO: check content type == json or empty (if docker accepts empty)
	// if strings.HasSuffix(req.URL.Path, "/containers/create") {
	// 	bodyData, err := io.ReadAll(io.LimitReader(req.Body, dockerProxyRequestBodyLimit))
	// 	if err != nil {
	// 		return nil, fmt.Errorf("read request body: %w", err)
	// 	}

	// 	var body dockertypes.FullContainerCreateRequest
	// 	err = json.Unmarshal(bodyData, &body)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("unmarshal request body: %w", err)
	// 	}

	// 	err = p.filterContainerCreate(req, &body)
	// 	if err != nil {
	// 		return nil, fmt.Errorf("filter container create: %w", err)
	// 	}
	// }

	return nil
}

func (p *DockerProxy) filterContainerCreate(req *http.Request, body *dockertypes.FullContainerCreateRequest) error {
	// translate all volume paths to host paths
	if body.HostConfig != nil {
		/*
			for i, bind := range body.HostConfig.Binds {
				src, destAndFlags, foundDestAndFlags := strings.Cut(bind, ":")
				if !foundDestAndFlags {
					continue
				}

				if !strings.HasPrefix(src, "/") || strings.HasPrefix(src, "/var/run") || strings.HasPrefix(src, "/var/lib") || strings.HasPrefix(src, mounts.Opt) || strings.HasPrefix(src, "/mnt/") {
					continue
				}

				body.HostConfig.Binds[i] = mounts.Virtiofs + src + ":" + destAndFlags
			}
		*/
	}

	newData, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request body: %w", err)
	}

	// close old request body
	req.Body.Close()
	// replace with new buffered data
	req.Body = io.NopCloser(bytes.NewReader(newData))
	req.ContentLength = int64(len(newData))
	return nil
}

func (p *DockerProxy) filterResponseBody(resp *http.Response, dst io.Writer, src io.Reader, state *RequestState) error {
	/*
		shouldFilter := false
		if strings.HasSuffix(resp.Request.URL.Path, "/json") {
			shouldFilter = false
		}

		if shouldFilter {
			limitReader := io.LimitReader(src, dockerProxyRequestBodyLimit)
			bodyData, err := io.ReadAll(limitReader)
			if err != nil {
				return fmt.Errorf("read response body: %w", err)
			}

			// TODO
			var body any
			err = json.Unmarshal(bodyData, &body)
			if err != nil {
				return fmt.Errorf("unmarshal response body: %w", err)
			}

			newData, err := json.Marshal(body)
			if err != nil {
				return fmt.Errorf("marshal response body: %w", err)
			}

			_, err = dst.Write(newData)
			return err
		}*/
	_, err := tcppump.CopyBuffer(dst, src, nil)
	if err != nil {
		return err
	}

	return nil
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

func (p *DockerProxy) RunTCP() error {
	for {
		clientConn, err := p.tcpListener.Accept()
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

func (p *DockerProxy) RunUnix() error {
	for {
		clientConn, err := p.unixListener.Accept()
		if err != nil {
			return err
		}

		go func() {
			err := p.serveConn(clientConn)
			if err != nil && !errors.Is(err, unix.EPIPE) {
				logrus.WithError(err).Error("docker conn failed")
			}
		}()
	}
}

func (p *DockerProxy) Close() error {
	p.tcpListener.Close()
	p.unixListener.Close()
	return nil
}
