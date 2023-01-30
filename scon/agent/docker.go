package agent

import (
	"context"
	"net"

	"github.com/docker/docker/api/types"
	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
)

func (a *AgentServer) CheckDockerIdle(_ None, reply *bool) error {
	// only includes running
	containers, err := a.dockerClient.ContainerList(context.TODO(), types.ContainerListOptions{})
	if err != nil {
		return err
	}

	*reply = len(containers) == 0
	return nil
}

func (a *AgentServer) HandleDockerConn(fdxSeq uint64, _ *None) error {
	// receive fd
	file, err := a.fdx.RecvFile(fdxSeq)
	if err != nil {
		return err
	}
	defer file.Close()

	extConn, err := net.FileConn(file)
	if err != nil {
		return err
	}
	defer extConn.Close()

	// dial unix socket
	dockerConn, err := net.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}
