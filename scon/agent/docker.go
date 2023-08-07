package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/orbstack/macvirt/scon/agent/tcpfwd"
	"github.com/orbstack/macvirt/scon/hclient"
	"github.com/orbstack/macvirt/scon/sgclient"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/syncx"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/scon/util/netx"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/conf/ports"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/orbstack/macvirt/vmgr/vnet/netconf"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"
)

const (
	dockerRefreshDebounce = 100 * time.Millisecond
	// TODO: skip debounce when GUI action in progress
	dockerUIEventDebounce = 50 * time.Millisecond

	dockerDefaultBridgeNetwork = "bridge"

	// from documentation test net 2
	DockerNetMigrationBip  = "203.0.113.97/24"
	DockerNetMigrationFlag = "/etc/docker/.orb_migrate_networks"

	DockerBridgeMirrorPrefix = ".orbmirror"
)

type DockerAgent struct {
	mu      syncx.Mutex
	client  *dockerclient.Client
	Running syncx.CondBool

	host *hclient.Client
	scon *sgclient.Client

	containerBinds map[string][]string
	lastContainers []dockertypes.ContainerSummaryMin // minimized struct to save memory
	lastNetworks   []dockertypes.Network

	// refreshing w/ debounce+diff ensures consistent snapshots
	containerRefreshDebounce syncx.FuncDebounce
	networkRefreshDebounce   syncx.FuncDebounce
	uiEventDebounce          syncx.FuncDebounce
	pendingUIEntities        []dockertypes.UIEntity

	eventsConn io.Closer

	dirSyncMu       syncx.Mutex
	dirSyncListener net.Listener
	dirSyncJobs     map[uint64]chan error
}

func NewDockerAgent() *DockerAgent {
	dockerAgent := &DockerAgent{
		// use default unix socket
		client: dockerclient.NewWithHTTP(&http.Client{
			// no timeout - we do event monitoring
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", "/var/run/docker.sock")
				},
				// idle conns are ok here because we get frozen along with docker
				MaxIdleConns: 2,
			},
		}, nil),

		Running:        syncx.NewCondBool(),
		containerBinds: make(map[string][]string),
		dirSyncJobs:    make(map[uint64]chan error),
	}

	dockerAgent.containerRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshContainers()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh containers")
		}
	})
	dockerAgent.networkRefreshDebounce = syncx.NewFuncDebounce(dockerRefreshDebounce, func() {
		err := dockerAgent.refreshNetworks()
		if err != nil {
			logrus.WithError(err).Error("failed to refresh networks")
		}
	})
	dockerAgent.uiEventDebounce = syncx.NewFuncDebounce(dockerUIEventDebounce, func() {
		err := dockerAgent.doSendUIEvent()
		if err != nil {
			logrus.WithError(err).Error("failed to send UI event")
		}
	})

	return dockerAgent
}

/*
 * Public RPC API
 */

func (a *AgentServer) DockerCheckIdle(_ None, reply *bool) error {
	// only includes running
	var containers []dockertypes.ContainerSummaryMin
	err := a.docker.client.Call("GET", "/containers/json", nil, &containers)
	if err != nil {
		return err
	}

	*reply = len(containers) == 0
	return nil
}

func (a *AgentServer) DockerHandleConn(fdxSeq uint64, _ *None) error {
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
	a.docker.Running.Wait()

	// dial unix socket
	dockerConn, err := net.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer dockerConn.Close()

	tcpfwd.Pump2SpTcpUnix(extConn.(*net.TCPConn), dockerConn.(*net.UnixConn))
	return nil
}

func (a *AgentServer) DockerWaitStart(_ None, _ *None) error {
	a.docker.Running.Wait()
	return nil
}

func readUntilResponseEnd(conn io.Reader, trailer string) (io.ReadWriter, error) {
	var respBuf bytes.Buffer
	var chBuf [1]byte
	for {
		// read byte-by-byte
		n, err := conn.Read(chBuf[:])
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, fmt.Errorf("short read")
		}

		// write to buffer
		respBuf.Write(chBuf[:])

		// check for end: '\r\n\r\n'
		rawBuf := respBuf.Bytes()
		if len(rawBuf) >= len(trailer) && string(rawBuf[len(rawBuf)-len(trailer):]) == trailer {
			return &respBuf, nil
		}
	}
}

func (a *AgentServer) DockerMigrationLoadImage(params types.InternalDockerMigrationLoadImageRequest, _ *None) error {
	remoteConn, err := netx.Dial("tcp", netconf.SecureSvcIP4+":"+strconv.Itoa(ports.SecureSvcDockerRemoteCtx))
	if err != nil {
		return err
	}
	defer remoteConn.Close()

	// make the request, then splice between /var/run/docker.sock and host rctx tcp
	query := url.Values{
		"names": params.RemoteImageNames,
	}
	req, err := http.NewRequest("GET", "/images/get?"+query.Encode(), nil)
	if err != nil {
		return err
	}
	// force close after chunked, so we can splice
	// Docker won't give us anything but chunked (identity = not implemented)
	req.Header.Set("Connection", "close")
	err = req.Write(remoteConn)
	if err != nil {
		return err
	}

	// make a fake reader that stops at \r\n\r\n, so we don't cut into the chunked data
	// http.ReadResponse only takes bufio reader
	remoteRespBuf, err := readUntilResponseEnd(remoteConn, "\r\n\r\n")
	if err != nil {
		return fmt.Errorf("read & buffer response: %w", err)
	}

	// read response
	remoteResp, err := http.ReadResponse(bufio.NewReader(remoteRespBuf), req)
	if err != nil {
		return err
	}

	// check status
	if remoteResp.StatusCode != http.StatusOK {
		// add the rest of the response body for reading error
		io.Copy(remoteRespBuf, remoteConn)
		return fmt.Errorf("(remote) %w", dockerclient.ReadError(remoteResp))
	}

	// disable nodelay now that http part is over
	if tcpConn, ok := remoteConn.(*net.TCPConn); ok {
		tcpConn.SetNoDelay(false)
	}

	// open local conn
	localConn, err := netx.Dial("unix", "/var/run/docker.sock")
	if err != nil {
		return err
	}
	defer localConn.Close()

	// make local req
	// raw data to get control over headers
	_, err = localConn.Write([]byte(`POST /images/load HTTP/1.1
Host: docker
User-Agent: orb-agent/1
Accept: */*
Content-Type: application/x-tar
Transfer-Encoding: chunked
Connection: close
Expect: 100-continue

`))
	if err != nil {
		return err
	}

	// read response
	localResp1, err := http.ReadResponse(bufio.NewReader(localConn), nil)
	if err != nil {
		return err
	}

	// check status
	if localResp1.StatusCode != http.StatusContinue {
		return fmt.Errorf("(local) %w", dockerclient.ReadError(localResp1))
	}

	// splice chunked data
	buf := make([]byte, tcpfwd.BufferSize)
	io.CopyBuffer(localConn, remoteConn, buf)

	// read response
	localResp2, err := http.ReadResponse(bufio.NewReader(localConn), nil)
	if err != nil {
		return err
	}

	// check status
	if localResp2.StatusCode != http.StatusOK {
		return fmt.Errorf("(local) %w", dockerclient.ReadError(localResp2))
	}

	// read body
	err = dockerclient.ReadStream(localResp2.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	return nil
}

func (a *AgentServer) DockerMigrationRunSyncServer(params types.InternalDockerMigrationRunSyncServerRequest, _ *None) error {
	a.docker.dirSyncMu.Lock()
	oldListener := a.docker.dirSyncListener
	if oldListener != nil {
		oldListener.Close()
	}

	// start the listener to get proxied to mac
	listener, err := a.localTCPRegistry.Listen(uint16(params.Port))
	if err != nil {
		return err
	}
	a.docker.dirSyncListener = listener
	a.docker.dirSyncJobs = make(map[uint64]chan error)
	a.docker.dirSyncMu.Unlock()

	go func() {
		defer listener.Close()

		for {
			// next conn: pass socket fd to tar and wait for it
			conn, err := listener.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					logrus.WithError(err).Error("failed to accept sync connection")
				}
				return
			}

			go func(conn net.Conn) {
				var jobID uint64
				err := func() error {
					defer conn.Close()

					// read
					reqReader, err := readUntilResponseEnd(conn, "\n")
					if err != nil {
						return fmt.Errorf("read request: %w", err)
					}

					// decode json
					var req types.InternalDockerMigrationSyncDirsRequest
					err = json.NewDecoder(reqReader).Decode(&req)
					if err != nil {
						return fmt.Errorf("decode request: %w", err)
					}
					jobID = req.JobID
					ch := make(chan error, 1)
					a.docker.dirSyncMu.Lock()
					a.docker.dirSyncJobs[jobID] = ch
					a.docker.dirSyncMu.Unlock()

					// currently only supports one dest
					if len(req.Dirs) != 1 {
						return errors.New("only one dir supported")
					}
					dest := req.Dirs[0]

					// unset nodelay
					err = conn.(*net.TCPConn).SetNoDelay(false)
					if err != nil {
						return err
					}

					// is this a Docker connection?
					if dest == types.DockerMigrationSyncDirImageLoad {
						err = a.docker.client.StreamWrite("POST", "/images/load", conn)
						if err != nil {
							return fmt.Errorf("load image: %w", err)
						}

						return nil
					}

					// this is a dup
					connFile, err := conn.(*net.TCPConn).File()
					if err != nil {
						return err
					}
					// close early to avoid issue with disabling nonblock
					conn.Close()
					defer connFile.Close()

					// disable nonblock to avoid issues with tar
					connFile.Fd()

					// ensure dest exists
					err = os.MkdirAll(dest, 0755)
					if err != nil {
						return err
					}

					// spawn tar
					cmd := exec.Command("tar", "--numeric-owner", "--xattrs", "--xattrs-include=*", "-xf", "-")
					cmd.Dir = dest
					cmd.Stdin = connFile
					output, err := cmd.CombinedOutput()
					if err != nil {
						return fmt.Errorf("extract tar: %w; output: %s", err, string(output))
					}

					return nil
				}()
				if err != nil {
					logrus.WithError(err).Error("failed to sync dir")
				}

				a.docker.dirSyncMu.Lock()
				if ch, ok := a.docker.dirSyncJobs[jobID]; ok {
					ch <- err
				}
				a.docker.dirSyncMu.Unlock()
			}(conn)
		}
	}()

	return nil
}

func (a *AgentServer) DockerMigrationWaitSync(params types.InternalDockerMigrationWaitSyncRequest, _ *None) error {
	a.docker.dirSyncMu.Lock()
	ch, ok := a.docker.dirSyncJobs[params.JobID]
	a.docker.dirSyncMu.Unlock()
	if !ok {
		return errors.New("not running")
	}

	return <-ch
}

func (a *AgentServer) DockerMigrationStopSyncServer(_ None, _ *None) error {
	listener := a.docker.dirSyncListener
	if listener == nil {
		return errors.New("not running")
	}

	a.docker.dirSyncListener = nil
	return listener.Close()
}

/*
 * Private - Docker agent
 */

func (d *DockerAgent) PostStart() error {
	// docker-init oom score adj
	// dockerd's score is set via cmdline argument
	err := os.WriteFile("/proc/1/oom_score_adj", []byte(oomScoreAdjCriticalGuest), 0644)
	if err != nil {
		return err
	}

	// wait for Docker API to start
	err = util.WaitForRunPathExist("/var/run/docker.sock")
	if err != nil {
		return err
	}

	// check for migration flag
	if origConfigJson, err := os.ReadFile(DockerNetMigrationFlag); err == nil {
		// this is the signal that we need to migrate
		err = d.migrateConflictNetworks(origConfigJson)
		if err != nil {
			return err
		}

		// great, migration successful, flag deleted to prevent recursion.
		// enter PostStart again to continue
		return d.PostStart()
	}

	d.Running.Set(true)

	// start docker event monitor
	go func() {
		hConn, err := net.Dial("unix", mounts.HcontrolSocket)
		if err != nil {
			logrus.WithError(err).Error("failed to connect to hcontrol")
			return
		}

		d.host, err = hclient.New(hConn)
		if err != nil {
			logrus.WithError(err).Error("failed to create hclient")
			return
		}

		sConn, err := net.Dial("unix", mounts.SconGuestSocket)
		if err != nil {
			logrus.WithError(err).Error("failed to connect to scon guest")
			return
		}
		d.scon, err = sgclient.New(sConn)
		if err != nil {
			logrus.WithError(err).Error("failed to create scon guest client")
			return
		}

		err = d.monitorEvents()
		if err != nil {
			logrus.WithError(err).Error("failed to monitor Docker events")
		}
	}()

	return nil
}

func (d *DockerAgent) killContainers() error {
	// kill containers for faster shutdown. min docker shutdown timeout = 6s (1 + 5s)
	// good programs should be safe to kill in case of power loss anyway, as long as we do it atomically like this
	// let docker daemon shut down cleanly due to bolt DBs and image db
	logrus.Debug("killing containers")
	cgroups, err := os.ReadDir("/sys/fs/cgroup/docker")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// no cgroups, no containers
			return nil
		} else {
			return err
		}
	}
	for _, cgroup := range cgroups {
		if !cgroup.IsDir() {
			continue
		}

		err = os.WriteFile("/sys/fs/cgroup/docker/"+cgroup.Name()+"/cgroup.kill", []byte("1"), 0644)
		if err != nil {
			// ENOENT: it may already be dead
			if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}

	return nil
}

func (d *DockerAgent) OnStop() error {
	logrus.Debug("stopping docker event monitor")
	if d.eventsConn != nil {
		d.eventsConn.Close()
	}

	err := d.killContainers()
	if err != nil {
		return err
	}

	return nil
}

func checkIPAMConflict(ipam dockertypes.IPAM, target netip.Prefix) (bool, error) {
	for _, config := range ipam.Config {
		logrus.WithField("config", config).Debug("checking IPAM config")
		subnet, err := netip.ParsePrefix(config.Subnet)
		if err != nil {
			return false, err
		}

		if subnet.Overlaps(target) {
			// we have a conflict
			return true, nil
		}
	}

	return false, nil
}

func (d *DockerAgent) getFullContainers() ([]*dockertypes.ContainerJSON, error) {
	var minContainers []*dockertypes.ContainerSummaryMin
	err := d.client.Call("GET", "/containers/json?all=true", nil, &minContainers)
	if err != nil {
		return nil, fmt.Errorf("get containers: %w", err)
	}

	var containers []*dockertypes.ContainerJSON
	for _, c := range minContainers {
		var full dockertypes.ContainerJSON
		err = d.client.Call("GET", "/containers/"+c.ID+"/json", nil, &full)
		if err != nil {
			return nil, fmt.Errorf("get container: %w", err)
		}

		containers = append(containers, &full)
	}

	return containers, nil
}

func (d *DockerAgent) migrateConflictNetworks(origConfigJson []byte) error {
	var origConfig map[string]any
	err := json.Unmarshal(origConfigJson, &origConfig)
	if err != nil {
		return err
	}

	targetBipStr := origConfig["bip"].(string)
	logrus.WithField("targetBip", targetBipStr).Info("migrating networks")

	// the bip we want. not the current temp one
	targetBip, err := netip.ParsePrefix(targetBipStr)
	if err != nil {
		return err
	}

	// get all networks
	var networks []dockertypes.Network
	err = d.client.Call("GET", "/networks", nil, &networks)
	if err != nil {
		return fmt.Errorf("get networks: %w", err)
	}

	// get all containers. need full info to get network aliases
	allContainers, err := d.getFullContainers()
	if err != nil {
		return fmt.Errorf("get containers: %w", err)
	}

	// find ones that conflict with bip prefix, and deal with them
	for _, minNet := range networks {
		// we only look at local bridges with IPv4 that conflicts w/ bip, and default IPAM driver
		if minNet.Scope != "local" || minNet.Driver != "bridge" || minNet.IPAM.Driver != "default" {
			continue
		}
		// check if conflict
		logrus.WithField("network", minNet.Name).Debug("checking network")
		hasConflict, err := checkIPAMConflict(minNet.IPAM, targetBip)
		if err != nil {
			return fmt.Errorf("check IPAM conflict: %w", err)
		}
		if !hasConflict {
			continue
		}

		// need to migrate this one
		logrus.WithField("network", minNet.Name).Info("migrating network")

		// fetch full info
		var fullNet dockertypes.Network
		err = d.client.Call("GET", "/networks/"+minNet.ID, nil, &fullNet)
		if err != nil {
			return fmt.Errorf("get network: %w", err)
		}

		// filter for containers
		// fullNet.Containers only includes running, so must look at allContainers
		netContainers := make(map[string]*dockertypes.NetworkEndpointSettings)
		for _, c := range allContainers {
			if c.NetworkSettings == nil {
				continue
			}
			if c.NetworkSettings.Networks == nil {
				continue
			}
			// don't trust the name, look through IDs
			for _, cnet := range c.NetworkSettings.Networks {
				if cnet.NetworkID == minNet.ID {
					netContainers[c.ID] = cnet
					break
				}
			}
		}

		// disconnect all containers
		logrus.WithField("network", minNet.Name).WithField("count", len(netContainers)).Info("disconnecting containers")
		for cid := range netContainers {
			logrus.WithField("cid", cid).Debug("disconnecting container")
			err = d.client.Call("POST", "/networks/"+minNet.ID+"/disconnect", dockertypes.NetworkDisconnectRequest{
				Container: cid,
				Force:     true,
			}, nil)
			if err != nil {
				// fatal. can't proceed if stuck
				return fmt.Errorf("disconnect container: %w", err)
			}
		}

		// delete the network
		err = d.client.Call("DELETE", "/networks/"+minNet.ID, nil, nil)
		if err != nil {
			return fmt.Errorf("delete network: %w", err)
		}

		// create new network with the same flags
		logrus.WithField("network", minNet.Name).Info("recreating network")
		var newNetResp dockertypes.NetworkCreateResponse
		newNetReq := fullNet
		newNetReq.ID = ""
		newNetReq.Created = ""
		newNetReq.Scope = ""
		newNetReq.Containers = nil
		newNetReq.CheckDuplicate = false // make sure it succeeds
		// discard conflicting IPv4 IPAM entries
		var newIPAMConfig []dockertypes.IPAMConfig
		for _, config := range newNetReq.IPAM.Config {
			subnet, err := netip.ParsePrefix(config.Subnet)
			if err != nil {
				return fmt.Errorf("parse subnet: %w", err)
			}

			if subnet.Overlaps(targetBip) {
				// we have a conflict
				continue
			}

			newIPAMConfig = append(newIPAMConfig, config)
		}
		newNetReq.IPAM.Config = newIPAMConfig
		err = d.client.Call("POST", "/networks/create", &newNetReq, &newNetResp)
		if err != nil {
			// oops, we probably ran out of pools...
			// try to restore the old one
			logrus.WithError(err).WithField("network", minNet.Name).Error("failed to recreate network, restoring")
			err = d.client.Call("POST", "/networks/create", &fullNet, &newNetResp)
			if err != nil {
				// fatal: if can't restore then it's broken
				return fmt.Errorf("restore network: %w", err)
			}

			// successfully restored. proceed to reconnect back, knowing that the migration failed to resolve conflicts
			// it's better than destroying data
		}

		// reconnect all containers
		logrus.WithField("network", minNet.Name).WithField("count", len(netContainers)).Info("reconnecting containers")
		for cid, endpointConfig := range netContainers {
			logrus.WithField("cid", cid).Debug("reconnecting container")
			err = d.client.Call("POST", "/networks/"+newNetResp.ID+"/connect", dockertypes.NetworkConnectRequest{
				Container: cid,
				EndpointConfig: &dockertypes.NetworkEndpointSettings{
					// only a few fields, exclude anything IP-related
					Links: endpointConfig.Links,
					// for Docker Compose sandbox DNS resolver
					Aliases: endpointConfig.Aliases,
				},
			}, nil)
			if err != nil {
				// not fatal but unexpected. too late to revert
				logrus.WithError(err).WithField("cid", cid).Error("failed to reconnect container")
			}
		}

		// fetch new full net to see where it went (for debug)
		var newFullNet dockertypes.Network
		err = d.client.Call("GET", "/networks/"+newNetResp.ID, nil, &newFullNet)
		if err != nil {
			return fmt.Errorf("get new network: %w", err)
		}

		logrus.WithField("from", minNet.IPAM.Config).WithField("to", newFullNet.IPAM.Config).Info("moved network")
	}

	// migration complete. remove flag, rewrite config, and restart dockerd
	logrus.Info("migration complete, restarting")
	err = os.Remove(DockerNetMigrationFlag)
	if err != nil {
		return err
	}

	// restore orig config to set correct bip & pools
	err = os.WriteFile("/etc/docker/daemon.json", origConfigJson, 0644)
	if err != nil {
		return err
	}

	// restart dockerd:
	// tini > simplevisor > dockerd
	// first delete socket to prevent race when PreStart is called again
	_ = os.Remove("/var/run/docker.sock")
	// kill tini with SIGUSR2. it'll forward
	err = unix.Kill(1, unix.SIGUSR2)
	if err != nil {
		return err
	}

	// kill containers to speed it up
	err = d.killContainers()
	if err != nil {
		return err
	}

	return nil
}

func (d *DockerAgent) refreshContainers() error {
	// no mu needed: FuncDebounce has mutex

	// only includes running
	var newContainers []dockertypes.ContainerSummaryMin
	err := d.client.Call("GET", "/containers/json", nil, &newContainers)
	if err != nil {
		return err
	}

	// diff
	added, removed := util.DiffSlicesKey[string](d.lastContainers, newContainers)

	// add first
	for _, c := range added {
		err = d.onContainerStart(c)
		if err != nil {
			logrus.WithError(err).Error("failed to add container")
		}
	}

	// then remove
	for _, c := range removed {
		err = d.onContainerStop(c)
		if err != nil {
			logrus.WithError(err).Error("failed to remove container")
		}
	}

	// tell scon
	err = d.scon.OnDockerContainersChanged(sgtypes.DockerContainersDiff{
		Added:   added,
		Removed: removed,
	})
	if err != nil {
		logrus.WithError(err).Error("failed to update scon containers")
	}

	d.lastContainers = newContainers
	return nil
}

func compareNetworks(a, b dockertypes.Network) bool {
	// always rank default bridge network first
	if a.Name == dockerDefaultBridgeNetwork {
		return true
	} else if b.Name == dockerDefaultBridgeNetwork {
		return false
	}

	return a.Name < b.Name
}

func findLink(links []netlink.Link, name string) netlink.Link {
	for _, l := range links {
		if l.Attrs().Name == name {
			return l
		}
	}
	return nil
}

func (d *DockerAgent) filterNewNetworks(nets []dockertypes.Network) ([]dockertypes.Network, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, fmt.Errorf("list links: %w", err)
	}

	var newNets []dockertypes.Network
	for _, n := range nets {
		// we only deal with local + bridge
		if n.Driver != "bridge" || n.Scope != "local" {
			continue
		}

		// must have 1+ active containers
		// but only full network obj has Containers and that's kinda expensive, so check interface membership instead
		ifName := dockerNetworkToInterfaceName(&n)

		// find index of bridge
		bridgeLink := findLink(links, ifName)
		if bridgeLink == nil {
			logrus.WithField("network", n.Name).Error("bridge not found")
			continue
		}
		bridgeIndex := bridgeLink.Attrs().Index

		// check if there are any containers (veth) attached
		hasMembers := false
		for _, l := range links {
			attrs := l.Attrs()
			if attrs.MasterIndex == bridgeIndex && !strings.HasPrefix(attrs.Name, DockerBridgeMirrorPrefix) {
				hasMembers = true
				break
			}
		}
		if !hasMembers {
			continue
		}

		newNets = append(newNets, n)
	}
	return newNets, nil
}

func (d *DockerAgent) refreshNetworks() error {
	// no mu needed: FuncDebounce has mutex

	var newNetworks []dockertypes.Network
	err := d.client.Call("GET", "/networks", nil, &newNetworks)
	if err != nil {
		return err
	}

	// filter out networks with no active containers.
	// people can have a lot of old compose networks piled up, causing us to reach vmnet bridge limit
	newNetworks, err = d.filterNewNetworks(newNetworks)
	if err != nil {
		return err
	}

	// diff
	added, removed := util.DiffSlicesKey[string](d.lastNetworks, newNetworks)
	slices.SortStableFunc(added, compareNetworks)
	slices.SortStableFunc(removed, compareNetworks)

	// add first
	for _, n := range added {
		err = d.onNetworkAdd(n)
		if err != nil {
			logrus.WithError(err).Error("failed to add network")
		}
	}

	// then remove
	for _, n := range removed {
		err = d.onNetworkRemove(n)
		if err != nil {
			logrus.WithError(err).Error("failed to remove network")
		}
	}

	d.lastNetworks = newNetworks
	return nil
}

func (d *DockerAgent) triggerUIEvent(entity dockertypes.UIEntity) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !slices.Contains(d.pendingUIEntities, entity) {
		d.pendingUIEntities = append(d.pendingUIEntities, entity)
	}
	d.uiEventDebounce.Call()
}

func (d *DockerAgent) doSendUIEvent() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	event := dockertypes.UIEvent{
		Changed: d.pendingUIEntities,
	}
	logrus.WithField("event", event).Debug("sending UI event")
	err := d.host.OnDockerUIEvent(&event)
	if err != nil {
		return err
	}

	d.pendingUIEntities = nil
	return nil
}

func (d *DockerAgent) monitorEvents() error {
	eventsConn, err := d.client.StreamRead("GET", "/events", nil)
	if err != nil {
		return err
	}
	defer eventsConn.Close()
	d.eventsConn = eventsConn

	// kick an initial refresh
	d.containerRefreshDebounce.Call()
	d.networkRefreshDebounce.Call()
	// also kick all initial UI events for menu bar bg start
	d.triggerUIEvent(dockertypes.UIEventContainer)
	d.triggerUIEvent(dockertypes.UIEventVolume)
	d.triggerUIEvent(dockertypes.UIEventImage)

	dec := json.NewDecoder(eventsConn)
	for {
		var event dockertypes.Event
		err := dec.Decode(&event)
		if err != nil {
			// EOF = Docker daemon stopped, or conn closed by PreStop
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
				return nil
			} else {
				return fmt.Errorf("decode json: %w", err)
			}
		}

		logrus.WithField("event", event).Debug("engine event")
		switch event.Type {
		case "container":
			switch event.Action {
			case "create", "start", "die", "destroy":
				d.triggerUIEvent(dockertypes.UIEventContainer)
				d.containerRefreshDebounce.Call()
				// also need to trigger networks refresh, because networks depends on active containers
				d.networkRefreshDebounce.Call()
			}

		case "volume":
			d.triggerUIEvent(dockertypes.UIEventVolume)
			// there is no event for images

		case "network":
			switch event.Action {
			// "connect" and "disconnect" for dynamic bridge creation depending on active containers
			case "create", "destroy", "connect", "disconnect":
				// we only care about bridges
				if event.Actor.Attributes.Type == "bridge" {
					d.networkRefreshDebounce.Call()
				}
			}
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

func (d *DockerAgent) onContainerStart(ctr dockertypes.ContainerSummaryMin) error {
	cid := ctr.ID
	logrus.WithField("cid", cid).Debug("container started")

	// get container bind mounts
	var binds []string
	for _, m := range ctr.Mounts {
		if m.Type == dockertypes.MountTypeBind {
			binds = append(binds, m.Source)
		} else if m.Type == dockertypes.MountTypeVolume && m.Driver == "local" && util.IsMountpointSimple(m.Source) {
			// for volumes that are mount points, do "docker inspect" and check:
			// 1. driver = local
			// 2. o = (r)bind
			// IsMountpointSimple is ok because this is bind mount from a different src
			// no need to check if src is mac path because it's checked below
			// m.Source = volume's _data path
			// m.Name = volume name

			// get volume info
			var volInfo dockertypes.Volume
			err := d.client.Call("GET", "/volumes/"+m.Name, nil, &volInfo)
			if err != nil {
				logrus.WithError(err).WithField("cid", cid).WithField("volume", m.Name).Warn("failed to get volume info")
				continue
			}

			// check driver
			if volInfo.Driver != "local" {
				continue
			}

			// check mount options
			opts := strings.Split(volInfo.Options["o"], ",")
			if !slices.Contains(opts, "bind") && !slices.Contains(opts, "rbind") {
				continue
			}

			// device = src path
			binds = append(binds, volInfo.Options["device"])
		}
	}
	d.mu.Lock()
	d.containerBinds[cid] = binds
	d.mu.Unlock()

	// report to host
	logrus.WithField("cid", cid).WithField("binds", binds).Debug("adding container binds")
	for _, path := range binds {
		// path translation:
		path = translateDockerPathToMac(path)
		if path == "" {
			logrus.WithField("path", path).Debug("ignoring bind mount")
			continue
		}

		err := d.host.AddFsnotifyRef(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *DockerAgent) onContainerStop(ctr dockertypes.ContainerSummaryMin) error {
	cid := ctr.ID
	logrus.WithField("cid", cid).Debug("container stopped")

	// get container bind mounts
	d.mu.Lock()
	binds, ok := d.containerBinds[cid]
	if !ok {
		d.mu.Unlock()
		return nil
	}
	delete(d.containerBinds, cid)
	d.mu.Unlock()

	// report to host
	logrus.WithField("cid", cid).WithField("binds", binds).Debug("removing container binds")
	for _, path := range binds {
		// path translation:
		path = translateDockerPathToMac(path)
		if path == "" {
			logrus.WithField("path", path).Debug("ignoring bind mount")
			continue
		}

		err := d.host.RemoveFsnotifyRef(path)
		if err != nil {
			return err
		}
	}

	return nil
}

func dockerNetworkToInterfaceName(n *dockertypes.Network) string {
	if n.Name == "bridge" {
		return "docker0"
	} else {
		return "br-" + n.ID[:12]
	}
}

func dockerNetworkToBridgeConfig(n dockertypes.Network) (sgtypes.DockerBridgeConfig, bool) {
	// requirements:
	//   - ipv4, ipv6, or 4+6
	//   - ipv6 must be /64
	//   - max 1 of each network type
	//   - min 1 type
	var ip4Subnet netip.Prefix
	var ip4Gateway netip.Addr
	var ip6Subnet netip.Prefix
	for _, ipam := range n.IPAM.Config {
		subnet, err := netip.ParsePrefix(ipam.Subnet)
		if err != nil {
			logrus.WithField("subnet", ipam.Subnet).Warn("failed to parse network subnet")
			continue
		}

		if subnet.Addr().Is4() {
			if ip4Subnet.IsValid() {
				// duplicate v4 - not supported, could break
				return sgtypes.DockerBridgeConfig{}, false
			}

			ip4Subnet = subnet
			ip4Gateway, err = netip.ParseAddr(ipam.Gateway)
			if err != nil {
				// default = first addr in subnet
				// get the zero addr (masked), then add one
				ip4Gateway = subnet.Masked().Addr().Next()
			}
		} else if n.EnableIPv6 {
			// ignore v6 if not enabled
			if ip6Subnet.IsValid() {
				// duplicate v6 - not supported, could break
				return sgtypes.DockerBridgeConfig{}, false
			}

			// must be /64 - macOS doesn't support other prefix lens for vmnet
			if subnet.Bits() != 64 {
				// if not, then skip v6 - we may still be able to use v4
				continue
			}

			ip6Subnet = subnet
		}
	}

	// must have at least one
	if !ip4Subnet.IsValid() && !ip6Subnet.IsValid() {
		return sgtypes.DockerBridgeConfig{}, false
	}

	// resolve interface name
	ifName := dockerNetworkToInterfaceName(&n)

	return sgtypes.DockerBridgeConfig{
		IP4Subnet:          ip4Subnet,
		IP4Gateway:         ip4Gateway,
		IP6Subnet:          ip6Subnet,
		GuestInterfaceName: ifName,
	}, true
}

func (d *DockerAgent) onNetworkAdd(network dockertypes.Network) error {
	config, ok := dockerNetworkToBridgeConfig(network)
	if !ok {
		logrus.WithField("name", network.Name).Debug("ignoring network")
		return nil
	}

	logrus.WithField("name", network.Name).WithField("config", config).Info("adding network")
	err := d.scon.DockerAddBridge(config)
	if err != nil {
		return err
	}

	return nil
}

func (d *DockerAgent) onNetworkRemove(network dockertypes.Network) error {
	// this works because we have the full Network object from lastNetworks diff
	config, ok := dockerNetworkToBridgeConfig(network)
	if !ok {
		return nil
	}

	logrus.WithField("name", network.Name).WithField("config", config).Info("removing network")
	err := d.scon.DockerRemoveBridge(config)
	if err != nil {
		return err
	}

	return nil
}
