package wormclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	pb "github.com/orbstack/macvirt/scon/wormclient/generated"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
	"golang.org/x/term"
)

var errNeedRetry = errors.New("server stopped on remote host, retrying")

const registryImage = "registry.orb.local/wormhole:1"
const maxRetries = 3

// returns the docker daemon, isLocal, error
func GetDaemon(context string) (*dockerclient.DockerConnection, bool, error) {
	if context == "" {
		context = dockerclient.GetCurrentContext()
	}
	daemon, err := dockerclient.GetDockerDaemon(context)
	if err != nil {
		return nil, false, err
	}

	// check if the daemon is local by comparing with the orbstack socket
	orbSocket := conf.DockerSocket()
	url, err := url.Parse(daemon.Host)
	if err != nil {
		return nil, false, err
	}

	isLocal := url.Scheme == "unix" && url.Path == orbSocket
	return daemon, isLocal, nil
}

func GetDrmToken() (string, error) {
	keychainData, err := drmcore.ReadKeychainDrmState()
	if err != nil {
		return "", err
	}

	var keychainState drmtypes.PersistentState
	err = json.Unmarshal(keychainData, &keychainState)
	if err != nil {
		return "", err
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
		} else if err != errNeedRetry {
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

	// Optimistically create server container to potentially save an additional roundtrip request. If the server container
	// already exists, we can just attach to the current server container ID returned in the error response.
	pullingFromOverride := "Pulling remote debug image from OrbStack registry"
	serverContainerId, err := client.RunContainer(
		dockerclient.RunContainerOptions{
			Name:      "orbstack-wormhole",
			PullImage: true,
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

	reader, writer, err := client.ExecStream(serverContainerId, &dockertypes.ContainerExecCreateRequest{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/bin/wormhole-proxy"},
	})
	if err != nil {
		// the server may have been removed or stopped right after we inspected it; retry in those cases
		// 404: no such container
		// 409: container is paused
		if dockerclient.IsStatusError(err, 404) || dockerclient.IsStatusError(err, 409) {
			return nil, errNeedRetry
		} else {
			return nil, err
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
		if err == io.EOF {
			_, inspectErr := client.InspectContainer(serverContainerId)
			// if server no long exists (shutdown after we attached via exec), retry
			if dockerclient.IsStatusError(inspectErr, 404) {
				return nil, errNeedRetry
			} else {
				return nil, errors.New("client proxy exited unexpectedly")
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
