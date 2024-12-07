package wormclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	pb "github.com/orbstack/macvirt/scon/wormclient/generated"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"golang.org/x/term"
)

var errNeedRetry = errors.New("server stopped on remote host, retrying")

// note: we keep the client and server versions in sync (i.e. client 1.0.0 should pull server 1.0.0)
const registryImageDebug = "registry.orb.local/wormhole:" + Version
const registryImageRelease = "api-registry.orbstack.dev/wormhole:" + Version

const (
	versionMismatchExitCode = 123
	// https://docs.docker.com/engine/containers/run/#exit-status
	commandNotInvokedExitCode = 126
)

const maxRetries = 3
const execTimeout = 15 * time.Second

// returns the docker endpoint, isLocal, error
func GetDockerEndpoint(context string) (dockerclient.Endpoint, bool, error) {
	context = dockerclient.ResolveContextName(context)
	endpoint, err := dockerclient.GetDockerEndpoint(context)
	if err != nil {
		return dockerclient.Endpoint{}, false, err
	}

	// check if the docker endpoint is local by comparing with the orbstack socket
	orbSocket := conf.DockerSocket()
	url, err := url.Parse(endpoint.Host)
	if err != nil {
		return dockerclient.Endpoint{}, false, err
	}

	isLocal := url.Scheme == "unix" && url.Path == orbSocket
	return endpoint, isLocal, nil
}

func GetDrmToken() (string, error) {
	keychainData, err := drmcore.ReadKeychainDrmState()
	if err != nil {
		return "", err
	}

	if len(keychainData) == 0 {
		return "", errors.New("Please sign in to OrbStack using 'orb login' to use remote debugging.")
	}

	var keychainState drmtypes.PersistentState
	err = json.Unmarshal(keychainData, &keychainState)
	if err != nil {
		return "", fmt.Errorf("parse DRM state json: %w", err)
	}

	return keychainState.EntitlementToken, nil
}

func connectRemote(client *dockerclient.Client, drmToken string, retries int) (*RpcServer, error) {
	var server *RpcServer
	var err error

	for i := 0; i < retries; i++ {
		server, err = connectRemoteHelper(client, drmToken)
		if err == nil {
			return server, nil
		} else if !errors.Is(err, errNeedRetry) {
			return nil, err
		}
	}

	return nil, fmt.Errorf("failed to connect after %d retries: %w", retries, err)
}

func connectRemoteHelper(client *dockerclient.Client, drmToken string) (*RpcServer, error) {
	// Start wormhole server and establish a client connection. There are a few scenarios where a race can occur:
	//   - two clients start a server container at the same time, resulting in a name conflict. In this case,
	// the process that experiences the name conflict will continue normally with the new server container ID returned in the error response.
	//   - server container shuts down before we `docker exec client` into it. Retry.
	//   - client connects right before the server shuts down. We detect this if we receive an EOF from the server
	// before we receive an initial ACK message, and retry in this case.

	pullingFromOverride := "Pulling remote debug image from OrbStack registry"
	var registryImage string
	var pullImageMode dockerclient.PullImageMode

	if conf.Debug() {
		// always pull for development so we can change the wormhole image in-place
		pullImageMode = dockerclient.PullImageAlways
		registryImage = registryImageDebug
	} else {
		// for prod, only pull the image if it doesn't exist to avoid unnecessary registry requests. the client can safely
		// assume that the wormhole server image for any given version will remain static, and any updates will be pushed
		// in a newer version.
		pullImageMode = dockerclient.PullImageIfMissing
		registryImage = registryImageRelease
	}

	// Optimistically create server container to potentially save an additional roundtrip request. If the server container
	// already exists, we can just attach to the current server container ID returned in the error response.
	serverContainerId, err := client.RunContainer(
		dockerclient.RunContainerOptions{
			Name:      "orbstack-wormhole",
			PullImage: pullImageMode,
			PullImageOpts: &dockerclient.PullImageOptions{
				ProgressOut:         os.Stderr,
				IsTerminal:          term.IsTerminal(fdStderr),
				TerminalFd:          fdStderr,
				PullingFromOverride: &pullingFromOverride,
			},
		},
		&dockertypes.ContainerCreateRequest{
			Image:      registryImage,
			Entrypoint: []string{"/bin/wormhole-server"},
			HostConfig: &dockertypes.ContainerHostConfig{
				Privileged:   true,
				Binds:        []string{"wormhole-data:/data"},
				CgroupnsMode: "host",
				PidMode:      "host",
				NetworkMode:  "none",
				AutoRemove:   true,
			},
		})
	if err != nil {
		// if the server container already exists, use the container ID returned in the error response
		// err: ...name /orbstack-wormhole is already in use by container "<container-id>". ...
		if dockerclient.IsStatusError(err, 409) {
			serverContainerId = strings.Split(err.Error(), "already in use by container \"")[1]
			serverContainerId = strings.Split(serverContainerId, `".`)[0]
		} else {
			return nil, err
		}
	}

	var reader io.Reader
	var writer io.WriteCloser
	var execId string
	var hasTimedOut atomic.Bool

	errorCh := make(chan error, 1)
	go func() {
		reader, writer, execId, err = client.ExecStream(serverContainerId, &dockertypes.ContainerExecCreateRequest{
			AttachStdin:  true,
			AttachStdout: true,
			AttachStderr: true,
			Cmd:          []string{"/bin/wormhole-proxy"},
			Env:          []string{fmt.Sprintf("WORMHOLE_CLIENT_VERSION=%s", Version)},
		})
		errorCh <- err

		// close connection to avoid leaking if we hit the timeout while establishing the stream
		if hasTimedOut.Load() && writer != nil {
			writer.Close()
		}
	}()

	select {
	case <-time.After(execTimeout):
		hasTimedOut.Store(true)

		// handle the possible race condition where the ExecStream goroutine exits right before the
		// timeout fires, causing a connection leak since `hasTimedOut` is not yet set.
		select {
		case <-errorCh:
			if writer != nil {
				writer.Close()
			}
		default:
		}

		// if the exec takes longer than execTimeout, the server may be in a stuck state (e.g. from containerd deadlock bugs). We should
		// kill the server container to avoid the container running indefinitely on the remote host.
		err := client.KillContainer(serverContainerId)
		if err == nil {
			return nil, fmt.Errorf("exec stream timeout: %w", errNeedRetry)
		} else {
			return nil, fmt.Errorf("exec stream timeout; killing server container: %w", err)
		}
	case err := <-errorCh:
		if err != nil {
			// the server may have been removed or stopped right after we inspected it; retry in those cases
			// 404: no such container
			// 409: container is paused
			if dockerclient.IsStatusError(err, 404) || dockerclient.IsStatusError(err, 409) {
				return nil, fmt.Errorf("exec to server: %w", errNeedRetry)
			} else {
				return nil, err
			}
		}
	}

	demuxReader, demuxWriter := io.Pipe()
	go func() {
		defer demuxReader.Close()
		defer demuxWriter.Close()
		defer writer.Close()
		dockerclient.DemuxOutput(reader, demuxWriter, nil)
	}()

	sessionStdin := writer
	sessionStdout := demuxReader

	server := RpcServer{reader: sessionStdout, writer: sessionStdin}

	// wait for server to acknowledge client.
	message := &pb.RpcServerMessage{}
	err = server.ReadMessage(message)
	if err != nil {
		// EOF means that the client attach session was abruptly closed. This may happen
		// due to wormhole-proxy crashing or the server container shutting down (before we've
		// received an acknowledgement). We should only retry in the latter case.
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			serverInspect, serverInspectErr := client.InspectContainer(serverContainerId)
			if dockerclient.IsStatusError(serverInspectErr, 404) || serverInspect.State.Status == "removing" {
				return nil, fmt.Errorf("server shutdown before acknowledging client: %w", errNeedRetry)
			}

			// check client exit status for more information if the server is still up
			execInspect, execInspectErr := client.ExecInspect(execId)
			if execInspectErr != nil {
				return nil, fmt.Errorf("client proxy exited unexpectedly")
			} else if execInspect.ExitCode == versionMismatchExitCode {
				return nil, fmt.Errorf("client version %s unsupported by debug server", Version)
			} else if execInspect.ExitCode == commandNotInvokedExitCode {
				return nil, fmt.Errorf("server shutdown before client connected: %w", errNeedRetry)
			} else {
				return nil, fmt.Errorf("client proxy exited unexpectedly with exit code %d", execInspect.ExitCode)
			}
		} else {
			return nil, err
		}
	}
	switch message.ServerMessage.(type) {
	case *pb.RpcServerMessage_ClientConnectAck:
		// at this point, the server has incremented the connection refcount and we can safely continue
		break
	default:
		return nil, errors.New("client did not receive acknowledgement from server")
	}

	return &server, nil
}
