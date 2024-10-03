package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"golang.org/x/sys/unix"
)

var (
	ErrContainerNotRunning = errors.New("container is not running")

	errNoSuchImage = errors.New("no such image")
)

type StartWormholeArgs struct {
	Target string
}

type StartWormholeResponse struct {
	InitPid    int
	RootfsSeq  uint64
	WorkingDir string
	Env        []string

	// if we created a container/image, return ID so caller can clean up
	State WormholeSessionState
}

type EndWormholeArgs struct {
	State WormholeSessionState
}

type WormholeSessionState struct {
	CreatedContainerID string
	CreatedImageID     string
}

func (d *DockerAgent) createWormholeImageContainer(image string) (string, error) {
	id, err := d.client.RunContainer(&dockertypes.ContainerConfig{
		Image: image,
		Entrypoint: []string{
			"/dev/shm/.orb-wormhole-stub",
		},
		Labels: map[string]string{
			"dev.orbstack.wormhole.type": "temp-image",
		},
		StopSignal: "SIGKILL",
		HostConfig: &dockertypes.ContainerHostConfig{
			AutoRemove: true,
			Binds: []string{
				mounts.WormholeStub + ":/dev/shm/.orb-wormhole-stub",
			},
		},
	}, false)
	if err != nil {
		var apiErr *dockerclient.APIError
		if errors.As(err, &apiErr) && strings.HasPrefix(apiErr.Message, "No such image:") {
			return "", errNoSuchImage
		}

		return "", err
	}

	return id, nil
}

// prep: get container's init pid and open its rootfs dirfd
func (a *AgentServer) DockerStartWormhole(args *StartWormholeArgs, reply *StartWormholeResponse) error {
	var initPid int
	var workingDir string
	var env []string
	var state WormholeSessionState
	if conf.Debug() && args.Target == sshtypes.WormholeIDDocker {
		// debug only: wormhole for docker machine (ovm)
		initPid = 1
		workingDir = "/"
		env = []string{}
	} else {
		// standard path: for docker containers
		ctr, err := a.docker.client.InspectContainer(args.Target)
		if err != nil {
			var apiErr *dockerclient.APIError
			if errors.As(err, &apiErr) && apiErr.HTTPStatus == 404 {
				// container not found. try interpreting target as an image and creating a container from it
				state.CreatedContainerID, err = a.docker.createWormholeImageContainer(args.Target)
				if err != nil {
					if errors.Is(err, errNoSuchImage) {
						return fmt.Errorf("no such container or image: %s", args.Target)
					} else {
						return err
					}
				}

				ctr, err = a.docker.client.InspectContainer(state.CreatedContainerID)
				if err != nil {
					return err
				}
			} else {
				return err
			}
		}

		// kata and gvisor runtimes won't work
		if strings.Contains(ctr.HostConfig.Runtime, "kata") || strings.Contains(ctr.HostConfig.Runtime, "gvisor") {
			return fmt.Errorf("unsupported container runtime: %s", ctr.HostConfig.Runtime)
		}

		if ctr.State.Pid == 0 {
			if state.CreatedContainerID != "" {
				return fmt.Errorf("newly-created container %s crashed", ctr.ID)
			}

			return ErrContainerNotRunning
		}

		initPid = ctr.State.Pid
		workingDir = ctr.Config.WorkingDir
		env = ctr.Config.Env
	}

	if initPid == 0 {
		return ErrContainerNotRunning
	}

	fd, err := unix.Open(fmt.Sprintf("/proc/%d/root", initPid), unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	seq, err := a.fdx.SendFdInt(fd)
	if err != nil {
		return err
	}

	*reply = StartWormholeResponse{
		RootfsSeq:  seq,
		InitPid:    initPid,
		WorkingDir: workingDir,
		Env:        env,
		State:      state,
	}

	return nil
}

func (a *AgentServer) DockerEndWormhole(args *EndWormholeArgs, reply *None) error {
	// delete container using auto-remove
	if args.State.CreatedContainerID != "" {
		err := a.docker.client.KillContainer(args.State.CreatedContainerID)
		if err != nil {
			return err
		}
	}

	// delete image
	if args.State.CreatedImageID != "" {
		err := a.docker.client.RemoveImage(args.State.CreatedImageID, true)
		if err != nil {
			return err
		}
	}

	return nil
}
