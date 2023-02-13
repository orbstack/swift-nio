package agent

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
)

const (
	dockerConnectInterval = 100 * time.Millisecond
	dockerConnectTimeout  = 5 * time.Second
)

func (a *AgentServer) CheckDockerIdle(_ None, reply *bool) error {
	// only includes running
	resp, err := a.dockerClient.Get("http://docker/containers/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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

func retryDial(network, addr string) (net.Conn, error) {
	var conn net.Conn
	var err error

	start := time.Now()
	for time.Since(start) < dockerConnectTimeout {
		conn, err = net.Dial(network, addr)
		if err == nil {
			return conn, nil
		}

		time.Sleep(dockerConnectInterval)
	}

	return nil, fmt.Errorf("retry dial timeout: %w", err)
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
	dockerConn, err := retryDial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}
