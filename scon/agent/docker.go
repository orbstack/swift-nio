package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path"
	"strings"
	"time"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
	"github.com/kdrag0n/macvirt/macvmgr/dockertypes"
	"github.com/kdrag0n/macvirt/scon/agent/tcpfwd"
	"github.com/kdrag0n/macvirt/scon/hclient"
	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

const (
	dockerRefreshDebounce = 100 * time.Millisecond
	dockerUIEventDebounce = 50 * time.Millisecond
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

	var containers []dockertypes.ContainerSummaryMin
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

func (a *AgentServer) dockerRefreshContainers() error {
	// no mu needed: synchronized by debounce

	// only includes running
	resp, err := a.dockerClient.Get("http://docker/containers/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return errors.New("docker API returned " + resp.Status)
	}

	var newContainers []dockertypes.ContainerSummaryMin
	err = json.NewDecoder(resp.Body).Decode(&newContainers)
	if err != nil {
		return err
	}

	// diff
	added, removed := util.DiffSlicesKey[string](a.dockerLastContainers, newContainers)

	// add first
	for _, c := range added {
		err = a.onDockerContainerStart(c)
		if err != nil {
			logrus.WithError(err).Error("failed to add Docker container")
		}
	}

	// then remove
	for _, c := range removed {
		err = a.onDockerContainerStop(c)
		if err != nil {
			logrus.WithError(err).Error("failed to remove Docker container")
		}
	}

	a.dockerLastContainers = newContainers
	return nil
}

func (a *AgentServer) dockerTriggerUIEvent(entity dockertypes.UIEntity) {
	a.dockerMu.Lock()
	defer a.dockerMu.Unlock()

	if !slices.Contains(a.dockerPendingUIEntities, entity) {
		a.dockerPendingUIEntities = append(a.dockerPendingUIEntities, entity)
	}
	a.dockerUIEventDebounce.Call()
}

func (a *AgentServer) dockerDoSendUIEvent() error {
	a.dockerMu.Lock()
	defer a.dockerMu.Unlock()

	event := dockertypes.UIEvent{
		Changed: a.dockerPendingUIEntities,
	}
	logrus.WithField("event", event).Debug("sending Docker UI event")
	err := a.dockerHost.OnDockerUIEvent(&event)
	if err != nil {
		return err
	}

	a.dockerPendingUIEntities = nil
	return nil
}

func (a *AgentServer) monitorDockerEvents() error {
	req, err := a.dockerClient.Get("http://unix/events")
	if err != nil {
		return err
	}

	// kick an initial refresh
	a.dockerRefreshDebounce.Call()

	dec := json.NewDecoder(req.Body)
	for {
		var event dockertypes.Event
		err := dec.Decode(&event)
		if err != nil {
			// EOF = Docker daemon stopped
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			} else {
				return fmt.Errorf("decode json: %w", err)
			}
		}

		switch event.Type {
		case "container":
			switch event.Action {
			case "start", "die":
				a.dockerTriggerUIEvent(dockertypes.UIEventContainer)
				a.dockerRefreshDebounce.Call()
			}

		case "volume":
			a.dockerTriggerUIEvent(dockertypes.UIEventVolume)
			// there is no event for images
		}
	}
}

func translateDockerPathToMac(p string) string {
	p = path.Clean(p)

	// if under /mnt/mac, translate
	if p == mounts.Virtiofs || strings.HasPrefix(p, mounts.Virtiofs+"/") {
		return strings.TrimPrefix(p, mounts.Virtiofs)
	}

	// if linked, do nothing
	// extra Docker /var/folders and /tmp links can be ignored because they link to virtiofs, and docker bind mount sources resolve links
	for _, linkPrefix := range mounts.LinkedPaths {
		if p == linkPrefix || strings.HasPrefix(p, linkPrefix+"/") {
			return p
		}
	}

	// otherwise skip
	return ""
}

func (a *AgentServer) onDockerContainerStart(ctr dockertypes.ContainerSummaryMin) error {
	cid := ctr.ID
	logrus.WithField("cid", cid).Debug("Docker container started")

	// get container bind mounts
	var binds []string
	for _, m := range ctr.Mounts {
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
		// path translation:
		path = translateDockerPathToMac(path)
		if path == "" {
			logrus.WithField("path", path).Debug("ignoring Docker bind mount")
			continue
		}

		err := a.dockerHost.AddFsnotifyRef(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *AgentServer) onDockerContainerStop(ctr dockertypes.ContainerSummaryMin) error {
	cid := ctr.ID
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
		// path translation:
		path = translateDockerPathToMac(path)
		if path == "" {
			logrus.WithField("path", path).Debug("ignoring Docker bind mount")
			continue
		}

		err := a.dockerHost.RemoveFsnotifyRef(path)
		if err != nil {
			return err
		}
	}

	return nil
}
