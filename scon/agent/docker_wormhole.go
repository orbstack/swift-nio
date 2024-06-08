package agent

import (
	"fmt"

	"github.com/orbstack/macvirt/scon/conf"
	"github.com/orbstack/macvirt/vmgr/vnet/services/hostssh/sshtypes"
	"golang.org/x/sys/unix"
)

type PrepWormholeArgs struct {
	ContainerID string
}

type PrepWormholeResponse struct {
	InitPid    int
	RootfsSeq  uint64
	WorkingDir string
	Env        []string
}

// prep: get container's init pid and open its rootfs dirfd
func (a *AgentServer) DockerPrepWormhole(args *PrepWormholeArgs, reply *PrepWormholeResponse) error {
	var initPid int
	var workingDir string
	var env []string
	if conf.Debug() && args.ContainerID == sshtypes.WormholeIDDocker {
		initPid = 1
		workingDir = "/"
		env = []string{}
	} else {
		ctr, err := a.docker.client.InspectContainer(args.ContainerID)
		if err != nil {
			return err
		}

		initPid = ctr.State.Pid
		workingDir = ctr.Config.WorkingDir
		env = ctr.Config.Env
	}

	if initPid == 0 {
		return fmt.Errorf("container %s is not running", args.ContainerID)
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

	*reply = PrepWormholeResponse{
		RootfsSeq:  seq,
		InitPid:    initPid,
		WorkingDir: workingDir,
		Env:        env,
	}

	return nil
}
