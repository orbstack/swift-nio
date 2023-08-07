package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
)

func readUntilResponseEnd(conn io.Reader, trailer string) (io.ReadWriter, error) {
	var respBuf bytes.Buffer
	var chBuf [1]byte
	for {
		// read byte-by-byte
		n, err := conn.Read(chBuf[:])
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, fmt.Errorf("short read")
		}

		// write to buffer
		respBuf.Write(chBuf[:])

		// check for end: '\r\n\r\n'
		rawBuf := respBuf.Bytes()
		if len(rawBuf) >= len(trailer) && string(rawBuf[len(rawBuf)-len(trailer):]) == trailer {
			return &respBuf, nil
		}
	}
}

func (a *AgentServer) DockerMigrationLoadImage(params types.InternalDockerMigrationLoadImageRequest, _ *None) error {
	remoteConn, err := netx.Dial("tcp", netconf.SecureSvcIP4+":"+strconv.Itoa(ports.SecureSvcDockerRemoteCtx))
	if err != nil {
		return err
	}
	defer remoteConn.Close()

	// make the request, then splice between /var/run/docker.sock and host rctx tcp
	query := url.Values{
		"names": params.RemoteImageNames,
	}
	req, err := http.NewRequest("GET", "/images/get?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	// force close after chunked, so we can splice
	// Docker won't give us anything but chunked (identity = not implemented)
	req.Header.Set("Connection", "close")
	err = req.Write(remoteConn)
	if err != nil {
		return err
	}

	// make a fake reader that stops at \r\n\r\n, so we don't cut into the chunked data
	// http.ReadResponse only takes bufio reader
	remoteRespBuf, err := readUntilResponseEnd(remoteConn, "\r\n\r\n")
	if err != nil {
		return fmt.Errorf("read & buffer response: %w", err)
	}

	// read response
	remoteResp, err := http.ReadResponse(bufio.NewReader(remoteRespBuf), req)
	if err != nil {
		return err
	}

	// check status
	if remoteResp.StatusCode != http.StatusOK {
		// add the rest of the response body for reading error
		io.Copy(remoteRespBuf, remoteConn)
		return fmt.Errorf("(remote) %w", dockerclient.ReadError(remoteResp))
	}

	// disable nodelay now that http part is over
	if tcpConn, ok := remoteConn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(false)
	}

	// open local conn
	localConn, err := netx.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer localConn.Close()

	// make local req
	// raw data to get control over headers
	_, err = localConn.Write([]byte(`POST /images/load HTTP/1.1
Host: docker
User-Agent: orb-agent/1
Accept: */*
Content-Type: application/x-tar
Transfer-Encoding: chunked
Connection: close
Expect: 100-continue

`))
	if err != nil {
		return err
	}

	// read response
	localResp1, err := http.ReadResponse(bufio.NewReader(localConn), nil)
	if err != nil {
		return err
	}

	// check status
	if localResp1.StatusCode != http.StatusContinue {
		return fmt.Errorf("(local) %w", dockerclient.ReadError(localResp1))
	}

	// splice chunked data
	buf := make([]byte, tcpfwd.BufferSize)
	io.CopyBuffer(localConn, remoteConn, buf)

	// read response
	localResp2, err := http.ReadResponse(bufio.NewReader(localConn), nil)
	if err != nil {
		return err
	}

	// check status
	if localResp2.StatusCode != http.StatusOK {
		return fmt.Errorf("(local) %w", dockerclient.ReadError(localResp2))
	}

	// read body
	err = dockerclient.ReadStream(localResp2.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	return nil
}

func (a *AgentServer) DockerMigrationRunSyncServer(params types.InternalDockerMigrationRunSyncServerRequest, _ *None) error {
	a.docker.dirSyncMu.Lock()
	oldListener := a.docker.dirSyncListener
	if oldListener != nil {
		oldListener.Close()
	}

	// start the listener to get proxied to mac
	listener, err := a.localTCPRegistry.Listen(uint16(params.Port))
	if err != nil {
		return err
	}
	a.docker.dirSyncListener = listener
	a.docker.dirSyncJobs = make(map[uint64]chan error)
	a.docker.dirSyncMu.Unlock()

	go func() {
		defer listener.Close()

		for {
			// next conn: pass socket fd to tar and wait for it
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					logrus.WithError(err).Error("failed to accept sync connection")
				}
				return
			}

			go func(conn net.Conn) {
				var jobID uint64
				err := func() error {
					defer conn.Close()

					// read
					reqReader, err := readUntilResponseEnd(conn, "\n")
					if err != nil {
						return fmt.Errorf("read request: %w", err)
					}

					// decode json
					var req types.InternalDockerMigrationSyncDirsRequest
					err = json.NewDecoder(reqReader).Decode(&req)
					if err != nil {
						return fmt.Errorf("decode request: %w", err)
					}
					jobID = req.JobID
					ch := make(chan error, 1)
					a.docker.dirSyncMu.Lock()
					a.docker.dirSyncJobs[jobID] = ch
					a.docker.dirSyncMu.Unlock()

					// currently only supports one dest
					if len(req.Dirs) != 1 {
						return errors.New("only one dir supported")
					}
					dest := req.Dirs[0]

					// unset nodelay
					err = conn.(*net.TCPConn).SetNoDelay(false)
					if err != nil {
						return err
					}

					// is this a Docker connection?
					if dest == types.DockerMigrationSyncDirImageLoad {
						err = a.docker.client.StreamWrite("POST", "/images/load", conn)
						if err != nil {
							return fmt.Errorf("load image: %w", err)
						}

						return nil
					}

					// this is a dup
					connFile, err := conn.(*net.TCPConn).File()
					if err != nil {
						return err
					}
					// close early to avoid issue with disabling nonblock
					conn.Close()
					defer connFile.Close()

					// disable nonblock to avoid issues with tar
					connFile.Fd()

					// ensure dest exists
					err = os.MkdirAll(dest, 0755)
					if err != nil {
						return err
					}

					// spawn tar
					cmd := exec.Command("tar", "--numeric-owner", "--xattrs", "--xattrs-include=*", "-xf", "-")
					cmd.Dir = dest
					cmd.Stdin = connFile
					output, err := cmd.CombinedOutput()
					if err != nil {
						return fmt.Errorf("extract tar: %w; output: %s", err, string(output))
					}

					return nil
				}()
				if err != nil {
					logrus.WithError(err).Error("failed to sync dir")
				}

				a.docker.dirSyncMu.Lock()
				if ch, ok := a.docker.dirSyncJobs[jobID]; ok {
					ch <- err
				}
				a.docker.dirSyncMu.Unlock()
			}(conn)
		}
	}()

	return nil
}

func (a *AgentServer) DockerMigrationWaitSync(params types.InternalDockerMigrationWaitSyncRequest, _ *None) error {
	a.docker.dirSyncMu.Lock()
	ch, ok := a.docker.dirSyncJobs[params.JobID]
	a.docker.dirSyncMu.Unlock()
	if !ok {
		return errors.New("not running")
	}

	return <-ch
}

func (a *AgentServer) DockerMigrationStopSyncServer(_ None, _ *None) error {
	listener := a.docker.dirSyncListener
	if listener == nil {
		return errors.New("not running")
	}

	a.docker.dirSyncListener = nil
	return listener.Close()
}
