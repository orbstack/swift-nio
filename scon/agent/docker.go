package agent

import (
	"encoding/json"
	"errors"
	"net"

	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
	"github.com/kdrag0n/macvirt/scon/util"
)

func (a *AgentServer) CheckDockerIdle(_ None, reply *bool) error {
	// only includes running
	resp, err := a.dockerClient.Get("http://docker/containers/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("docker API returned " + resp.Status)
	}

	var containers []struct {
		ID string `json:"Id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&containers)
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
	dockerConn, err := util.RetryDial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}
