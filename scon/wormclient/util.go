package wormclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	pb "github.com/orbstack/macvirt/scon/wormclient/generated"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/drm/drmcore"
	"github.com/orbstack/macvirt/vmgr/drm/drmtypes"
)

var errNeedRetry = errors.New("server stopped on remote host, retrying")

// registryImage should point to drm server; for locally testing, it's more convenient to just

// spin up a registry and push/pull to that registry instead
// const registryImage = "drmserver.orb.local/wormhole:latest"
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
		}
	}
	return nil, fmt.Errorf("failed to connect after %d retries: %w", retries, err)
}

func connectRemoteHelper(client *dockerclient.Client, drmToken string) (*RpcServer, error) {
	// Start wormhole server (if not running) and establish a client connection. There are a few scenarios where a race can occur:
	//   - two clients start a server container at the same time, resulting in a name conflict. In this case,
	// the process that experiences the name conflict will retry.
	//   - server container shuts down before we `docker exec client` into it. Retry.
	//   - client connects right before the server shuts down. We detect this if we receive an EOF from the server
	// before we receive an initial ACK message, and retry in this case.

	var serverContainerId string = ""
	// If the server container already exists and is running, the client should attach to it. Otherwise,
	// the client should remove any existing stopped server container and create a new one.
	containerInfo, err := client.InspectContainer("orbstack-wormhole")
	if err != nil && !dockerclient.IsStatusError(err, 404) {
		return nil, fmt.Errorf("failed to inspect container: %w", err)
	}
	if containerInfo != nil {
		if containerInfo.State.Running {
			serverContainerId = containerInfo.ID
		} else {
			err = client.RemoveContainer(containerInfo.ID, true)
			// the server may have been removed right after we inspected it, so safely ignore 404 no container
			// or 409 conflict (container is being removed)
			if err != nil && !dockerclient.IsStatusError(err, 404) && !dockerclient.IsStatusError(err, 409) {
				return nil, fmt.Errorf("failed to remove server container: %w", err)
			}
		}
	}

	if serverContainerId == "" {
		init := true
		// note: start server container with a constant name so that at most one server container exists
		serverContainerId, err = client.RunContainer(dockerclient.RunContainerOptions{Name: "orbstack-wormhole", PullImage: true},
			&dockertypes.ContainerCreateRequest{
				Image:      registryImage,
				Entrypoint: []string{"/wormhole-server"},
				HostConfig: &dockertypes.ContainerHostConfig{
					Privileged:   true,
					Binds:        []string{"wormhole-data:/data"},
					CgroupnsMode: "host",
					PidMode:      "host",
					AutoRemove:   true,
					Init:         &init,
				},
			})
		if err != nil {
			// potential name conflict (two servers started at the same time), retry
			if dockerclient.IsStatusError(err, 409) {
				return nil, fmt.Errorf("starting server container, %w", err)
			}

			return nil, err
		}
	}

	reader, writer, err := client.ExecStream(serverContainerId, &dockertypes.ContainerExecCreateRequest{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Cmd:          []string{"/wormhole-proxy"},
	})
	if err != nil {
		// the server may have been removed or stopped right after we inspected it; retry in those cases
		// 404: no such container
		// 409: container is paused
		if dockerclient.IsStatusError(err, 404) || dockerclient.IsStatusError(err, 409) {
			return nil, errNeedRetry
			// return nil, fmt.Errorf("exec client, %w", err)
		}
		return nil, err
	}

	demuxReader, demuxWriter := io.Pipe()
	go func() {
		defer demuxReader.Close()
		defer demuxWriter.Close()
		defer writer.Close()
		dockerclient.DemuxOutput(reader, demuxWriter)
	}()

	sessionStdin := writer
	sessionStdout := demuxReader

	server := RpcServer{reader: sessionStdout, writer: sessionStdin}

	// wait for server to acknowledge client.
	message := &pb.RpcServerMessage{}
	err = server.ReadMessage(message)
	if err != nil {
		// EOF means that the client attach session was abruptly closed. This may happen
		// if the `docker exec client` connects to the server container right before the
		// container is about to shut down. Retry.
		if err == io.EOF {
			// if retries == 1 {
			// 	fmt.Fprintf(os.Stderr, "%v\n", err)
			// 	os.Exit(1)
			// }
			// return connectRemote(client, drmToken, retries-1)
			fmt.Fprintf(os.Stderr, "server eof, retrying\n")
			return nil, errNeedRetry
		}
		return nil, err
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
