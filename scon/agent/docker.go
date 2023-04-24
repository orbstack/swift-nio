package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/dockertypes"
	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
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

	// wait for docker
	a.dockerRunning.Wait()

	// dial unix socket
	dockerConn, err := net.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2SpTcpUnix(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}

func (a *AgentServer) WaitForDockerStart(_ None, _ *None) error {
	a.dockerRunning.Wait()
	return nil
}

func (a *AgentServer) dockerPostStart() error {
	// wait for Docker API to start
	err := util.WaitForRunPathExist("/var/run/docker.sock")
	if err != nil {
		return err
	}

	a.dockerRunning.Set(true)

	// start docker event monitor
	go func() {
		conn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			logrus.WithError(err).Error("failed to connect to hcontrol")
			return
		}
		a.dockerHost, err = hclient.New(conn)
		if err != nil {
			logrus.WithError(err).Error("failed to create hclient")
			return
		}

		err = a.monitorDockerEvents()
		if err != nil {
			logrus.WithError(err).Error("failed to monitor Docker events")
		}
	}()

	return nil
}

func (a *AgentServer) monitorDockerEvents() error {
	req, err := a.dockerClient.Get("http://unix/events")
	if err != nil {
		return err
	}

	//TODO diff on start
	dec := json.NewDecoder(req.Body)
	for {
		var event dockertypes.Event
		err := dec.Decode(&event)
		if err != nil {
			return fmt.Errorf("decode json: %w", err)
		}

		if event.Type != "container" {
			continue
		}

		switch event.Action {
		case "start":
			err = a.onDockerContainerStart(event.Actor.ID)
			if err != nil {
				logrus.WithError(err).Error("failed to handle Docker container start")
			}
		case "die":
			err = a.onDockerContainerStop(event.Actor.ID)
			if err != nil {
				logrus.WithError(err).Error("failed to handle Docker container stop")
			}
		}
	}
}

func (a *AgentServer) onDockerContainerStart(cid string) error {
	logrus.WithField("cid", cid).Debug("Docker container started")

	// get container info
	resp, err := a.dockerClient.Get("http://docker/containers/" + cid + "/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("docker API returned " + resp.Status)
	}

	var info dockertypes.ContainerDetails
	err = json.NewDecoder(resp.Body).Decode(&info)
	if err != nil {
		return err
	}

	// get container bind mounts
	var binds []string
	for _, m := range info.Mounts {
		if m.Type == dockertypes.MountTypeBind {
			binds = append(binds, m.Source)
		}
	}
	a.dockerMu.Lock()
	a.dockerContainerBinds[cid] = binds
	a.dockerMu.Unlock()

	// report to host
	logrus.WithField("cid", cid).WithField("binds", binds).Debug("reporting Docker container binds")
	for _, path := range binds {
		err = a.dockerHost.AddFsnotifyRef(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *AgentServer) onDockerContainerStop(cid string) error {
	logrus.WithField("cid", cid).Debug("Docker container stopped")

	// get container bind mounts
	a.dockerMu.Lock()
	binds, ok := a.dockerContainerBinds[cid]
	if !ok {
		a.dockerMu.Unlock()
		return nil
	}
	delete(a.dockerContainerBinds, cid)
	a.dockerMu.Unlock()

	// report to host
	logrus.WithField("cid", cid).WithField("binds", binds).Debug("reporting Docker container binds")
	for _, path := range binds {
		err := a.dockerHost.RemoveFsnotifyRef(path)
		if err != nil {
			return err
		}
	}

	return nil
}
