package agent

import (
	"fmt"

	"golang.org/x/sys/unix"
)

type PrepWormholeArgs struct {
	ContainerID string
}

type PrepWormholeResponse struct {
	InitPid    int
	RootfsSeq  uint64
	WorkingDir string
}

// prep: get container's init pid and open its rootfs dirfd
func (a *AgentServer) DockerPrepWormhole(args *PrepWormholeArgs, reply *PrepWormholeResponse) error {
	ctr, err := a.docker.client.InspectContainer(args.ContainerID)
	if err != nil {
		return err
	}
	initPid := ctr.State.Pid
	if initPid == 0 {
		return fmt.Errorf("container %s is not running", args.ContainerID)
	}

	fd, err := unix.Open(fmt.Sprintf("/proc/%d/root", initPid), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	seq, err := a.fdx.SendFdInt(fd)
	if err != nil {
		return err
	}

	*reply = PrepWormholeResponse{
		RootfsSeq:  seq,
		InitPid:    initPid,
		WorkingDir: ctr.Config.WorkingDir,
	}

	return nil
}
