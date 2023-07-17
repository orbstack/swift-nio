package dmigrate

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

const (
	//migrationAgentImage = "ghcr.io/orbstack/dmigrate-agent:1"
	migrationAgentImage = "alpine:20230329"

	maxUnusedContainerAge = 6 * 30 * 24 * time.Hour // 6 months
)

type Migrator struct {
	srcClient  *dockerclient.Client
	destClient *dockerclient.Client

	mu           sync.Mutex
	networkIDMap map[string]string
}

type MigrateParams struct {
	IncludeContainers bool
	IncludeVolumes    bool
	IncludeImages     bool
	/* networks are implicit by containers */
}

type errorTracker struct {
	mu     sync.Mutex
	errors []error
}

func (e *errorTracker) Check() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	return errors.Join(e.errors...)
}

func NewMigratorWithUnixSockets(fromSocket, toSocket string) (*Migrator, error) {
	srcClient, err := dockerclient.NewWithUnixSocket(fromSocket)
	if err != nil {
		return nil, err
	}
	destClient, err := dockerclient.NewWithUnixSocket(toSocket)
	if err != nil {
		return nil, err
	}

	return &Migrator{
		srcClient:  srcClient,
		destClient: destClient,

		networkIDMap: make(map[string]string),
	}, nil
}

func (m *Migrator) Close() {
	m.srcClient.Close()
	m.destClient.Close()
}

func findFreeTCPPort() (int, error) {
	// zero-port listener
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()

	// get port
	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port, nil
}

func splitRepoTag(repoTag string) (string, string) {
	// last index, to deal with "localhost:5000/myimage:latest"
	sepPos := strings.LastIndex(repoTag, ":")
	if sepPos == -1 {
		return repoTag, "latest"
	}

	repoPart := repoTag[:sepPos]
	tagPart := repoTag[sepPos+1:]
	return repoPart, tagPart
}

func (m *Migrator) createAndStartContainer(client *dockerclient.Client, req *dockertypes.ContainerCreateRequest) (string, error) {
	// need to pull image first
	repoPart, tagPart := splitRepoTag(req.Image)
	err := client.Call("POST", "/images/create?fromImage="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
	if err != nil {
		return "", fmt.Errorf("pull image: %w", err)
	}

	// create --rm container
	var containerResp dockertypes.ContainerCreateResponse
	err = client.Call("POST", "/containers/create", req, &containerResp)
	if err != nil {
		return "", fmt.Errorf("create container: %w", err)
	}

	// start container
	err = client.Call("POST", "/containers/"+containerResp.ID+"/start", nil, nil)
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}

	return containerResp.ID, nil
}

func (m *Migrator) migrateOneImage(idx int, img dockertypes.Image, userName string) error {
	names := []string{img.ID}
	names = append(names, img.RepoTags...)

	// open export conn
	logrus.Infof("Migrating image %s", userName)
	err := scli.Client().InternalDockerStreamImage(types.InternalDockerStreamImageRequest{
		RemoteImageNames: names,
	})
	if err != nil {
		return fmt.Errorf("stream image: %w", err)
	}

	return nil
}

func (m *Migrator) submitImages(group *pond.TaskGroup, images []dockertypes.Image) error {
	for idx, img := range images {
		var userName string
		if len(img.RepoTags) > 0 {
			userName = img.RepoTags[0]
		} else {
			userName = img.ID
		}

		idx := idx
		img := img
		group.Submit(func() {
			err := m.migrateOneImage(idx, img, userName)
			if err != nil {
				panic(fmt.Errorf("image %s: %w", userName, err))
			}
		})
	}

	return nil
}

func (m *Migrator) migrateOneVolume(vol dockertypes.Volume) error {
	// create volume on dest
	logrus.Infof("Migrating volume %s", vol.Name)
	err := m.destClient.Call("POST", "/volumes/create", dockertypes.VolumeCreateRequest{
		Name:       vol.Name,
		DriverOpts: vol.Options,
		Labels:     vol.Labels,
	}, nil)
	if err != nil {
		return fmt.Errorf("create volume: %w", err)
	}

	// if it's a bind mount or any other type of mount that's not simple local, then we're done
	if _, ok := vol.Options["device"]; ok {
		return nil
	}

	// it's local. need to copy from _data/
	// find a free port for fwd (fwd on our side would go through agent)
	port, err := findFreeTCPPort()
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}

	// we're trying to get a direct connection with minimal copying
	// port forward connects directly to container via iptables
	// socat directly hooks up fds
	srcBinds := []string{vol.Name + ":/voldata:ro"}
	srcCommand := []string{"sh", "-c", `
		set -eufo pipefail
		apk add --no-cache tar socat
		cd /voldata
		socat TCP4-LISTEN:1024,reuseaddr,fork EXEC:"tar --numeric-owner --xattrs-include=* -cf - ."
	`}
	srcCid, err := m.createAndStartContainer(m.srcClient, &dockertypes.ContainerCreateRequest{
		Image: migrationAgentImage,
		Cmd:   srcCommand,
		HostConfig: &dockertypes.ContainerHostConfig{
			Privileged:  true,
			AutoRemove:  true,
			NetworkMode: "host",
			Binds:       srcBinds,
			PortBindings: map[string][]dockertypes.PortBinding{
				"1024/tcp": {
					{
						HostIP:   "127.0.0.1",
						HostPort: strconv.Itoa(port),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("run src command: %w", err)
	}
	defer func() {
		// kill. it'll remove itself
		err := m.srcClient.Call("POST", "/containers/"+srcCid+"/kill", nil, nil)
		if err != nil {
			// TODO does not exist = already exited
			logrus.Warnf("kill src container: %v", err)
		}
	}()

	// output

	// TODO wait for server to become available
	time.Sleep(1 * time.Second)

	// start and connect on dest
	destBinds := []string{vol.Name + ":/voldata:ro"}
	destCommand := []string{"sh", "-c", fmt.Sprintf(`
		set -eufo pipefail
		apk add --no-cache tar socat
		cd /voldata
		socat TCP4:host.docker.internal:%d EXEC:"tar --numeric-owner --xattrs-include=* -xf -"
	`, port)}
	destCid, err := m.createAndStartContainer(m.destClient, &dockertypes.ContainerCreateRequest{
		Image: migrationAgentImage,
		Cmd:   destCommand,
		HostConfig: &dockertypes.ContainerHostConfig{
			Privileged:  true,
			AutoRemove:  true,
			NetworkMode: "host",
			Binds:       destBinds,
		},
	})
	if err != nil {
		return fmt.Errorf("run dest command: %w", err)
	}

	// wait for exit
	err = m.destClient.Call("POST", "/containers/"+destCid+"/wait", nil, nil)
	if err != nil {
		// TODO does not exist = already exited
		return fmt.Errorf("wait for container: %w", err)
	}

	return nil
}

func (m *Migrator) submitVolumes(group *pond.TaskGroup, volumes []dockertypes.Volume) error {
	for _, vol := range volumes {
		vol := vol
		group.Submit(func() {
			err := m.migrateOneVolume(vol)
			if err != nil {
				panic(fmt.Errorf("volume %s: %w", vol.Name, err))
			}
		})
	}

	return nil
}

func (m *Migrator) migrateOneNetwork(n dockertypes.Network) error {
	// create network on dest, mostly same flags
	var newNetResp dockertypes.NetworkCreateResponse
	newNetReq := n
	newNetReq.ID = ""
	newNetReq.Created = ""
	newNetReq.Scope = ""
	newNetReq.Containers = nil
	newNetReq.CheckDuplicate = false // make sure it succeeds

	// if it's default Compose, then we can discard ipv4 net and use more-compatible net
	if n.Labels["com.docker.compose.network"] == "default" {
		var newIPAMConfig []dockertypes.IPAMConfig
		for _, config := range newNetReq.IPAM.Config {
			subnet, err := netip.ParsePrefix(config.Subnet)
			if err != nil {
				continue
			}

			// discard ipv4
			if subnet.Addr().Is4() {
				continue
			}

			newIPAMConfig = append(newIPAMConfig, config)
		}
		newNetReq.IPAM.Config = newIPAMConfig
	}

	err := m.destClient.Call("POST", "/networks/create", newNetReq, &newNetResp)
	if err != nil {
		return fmt.Errorf("create network: %w", err)
	}

	// save ID
	m.mu.Lock()
	m.networkIDMap[n.ID] = newNetResp.ID
	m.mu.Unlock()

	return nil
}

func (m *Migrator) submitNetworks(group *pond.TaskGroup, networks []dockertypes.Network) error {
	for _, n := range networks {
		n := n
		group.Submit(func() {
			err := m.migrateOneNetwork(n)
			if err != nil {
				panic(fmt.Errorf("network %s: %w", n.Name, err))
			}
		})
	}

	return nil
}

func (m *Migrator) migrateOneContainer(ctr dockertypes.ContainerSummary) error {
	// TODO start long-lived Go migration agent on src?

	// [src] fetch full info
	var fullCtr dockertypes.ContainerJSON
	err := m.srcClient.Call("GET", "/containers/"+ctr.ID+"/json", nil, &fullCtr)
	if err != nil {
		return fmt.Errorf("get src container: %w", err)
	}

	// [dest] create container
	var newCtrResp dockertypes.ContainerCreateResponse
	newCtrReq := dockertypes.FullContainerCreateRequest{
		ContainerConfig: fullCtr.Config,
		HostConfig:      fullCtr.HostConfig,
		NetworkingConfig: &dockertypes.NetworkNetworkingConfig{
			EndpointsConfig: fullCtr.NetworkSettings.Networks,
		},
	}
	// translate network IDs
	m.mu.Lock()
	for _, n := range newCtrReq.NetworkingConfig.EndpointsConfig {
		n.NetworkID = m.networkIDMap[n.NetworkID]
	}
	m.mu.Unlock()
	err = m.destClient.Call("POST", "/containers/create?name="+url.QueryEscape(fullCtr.Name)+"&platform="+url.QueryEscape(fullCtr.Platform), newCtrReq, &newCtrResp)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	// [dest] get new full info
	var newFullCtr dockertypes.ContainerJSON
	err = m.destClient.Call("GET", "/containers/"+newCtrResp.ID+"/json", nil, &newFullCtr)
	if err != nil {
		return fmt.Errorf("get dest container: %w", err)
	}

	// [src] pause container
	if ctr.State == "running" {
		err := m.srcClient.Call("POST", "/containers/"+ctr.ID+"/pause", nil, nil)
		if err != nil {
			return fmt.Errorf("pause container: %w", err)
		}
		defer func() {
			// [src] unpause container
			err := m.srcClient.Call("POST", "/containers/"+ctr.ID+"/unpause", nil, nil)
			if err != nil {
				logrus.Warnf("unpause container: %v", err)
			}
		}()
	}

	// TODO [src] tar the upper dir

	// deferred: [src] unpause container

	return nil
}

func (m *Migrator) submitContainers(group *pond.TaskGroup, containers []dockertypes.ContainerSummary) error {
	for _, ctr := range containers {
		var userName string
		if len(ctr.Names) > 0 {
			userName = ctr.Names[0]
		} else {
			userName = ctr.ID
		}

		ctr := ctr
		group.Submit(func() {
			err := m.migrateOneContainer(ctr)
			if err != nil {
				panic(fmt.Errorf("container %s: %w", userName, err))
			}
		})
	}

	return nil
}

func (m *Migrator) MigrateAll(params MigrateParams) error {
	// grab everything
	var images []dockertypes.Image
	err := m.srcClient.Call("GET", "/images/json", nil, &images)
	if err != nil {
		return fmt.Errorf("get images: %w", err)
	}
	var containers []dockertypes.ContainerSummary
	err = m.srcClient.Call("GET", "/containers/json?all=true", nil, &containers)
	if err != nil {
		return fmt.Errorf("get containers: %w", err)
	}
	var networks []dockertypes.Network
	err = m.srcClient.Call("GET", "/networks", nil, &networks)
	if err != nil {
		return fmt.Errorf("get networks: %w", err)
	}
	var volumesResp dockertypes.VolumeListResponse
	err = m.srcClient.Call("GET", "/volumes", nil, &volumesResp)
	if err != nil {
		return fmt.Errorf("get volumes: %w", err)
	}
	volumes := volumesResp.Volumes

	// FILTER CONTAINERS
	var filteredContainers []dockertypes.ContainerSummary
	for _, c := range containers {
		// skip migration image ones (won't work b/c migration img is excluded)
		if c.Image == migrationAgentImage {
			continue
		}

		// skip naturally exited containers, unless they're in a compose group
		if _, ok := c.Labels["com.docker.compose.project"]; !ok {
			if c.State == "exited" {
				continue
			}
		}

		// skip containers not used for >6 months (need to fetch full info)
		var fullCtr dockertypes.ContainerJSON
		err := m.srcClient.Call("GET", "/containers/"+c.ID+"/json", nil, &fullCtr)
		if err != nil {
			return fmt.Errorf("get src container: %w", err)
		}

		startedAt, err := time.Parse(time.RFC3339Nano, fullCtr.State.StartedAt)
		if err != nil {
			return fmt.Errorf("parse startedAt: %w", err)
		}
		finishedAt, err := time.Parse(time.RFC3339Nano, fullCtr.State.FinishedAt)
		if err != nil {
			return fmt.Errorf("parse finishedAt: %w", err)
		}
		if time.Since(startedAt) > maxUnusedContainerAge && time.Since(finishedAt) > maxUnusedContainerAge {
			logrus.WithField("container", c.ID).Debug("Skipping container: old and unused")
			continue
		}

		filteredContainers = append(filteredContainers, c)
	}

	// FILTER NETWORKS: must be Scope="local" Driver="bridge" and referenced by container
	// 1. build map of container-referenced networks
	containerUsedNets := make(map[string]struct{})
	for _, c := range filteredContainers {
		if c.NetworkSettings == nil {
			continue
		}
		if c.NetworkSettings.Networks == nil {
			continue
		}
		// don't trust the name, look through IDs
		for _, cnet := range c.NetworkSettings.Networks {
			containerUsedNets[cnet.NetworkID] = struct{}{}
		}
	}
	// 2. filter networks
	var filteredNetworks []dockertypes.Network
	for _, n := range networks {
		if n.Scope != "local" || n.Driver != "bridge" {
			continue
		}
		if _, ok := containerUsedNets[n.ID]; !ok {
			continue
		}
		filteredNetworks = append(filteredNetworks, n)
	}

	// FILTER VOLUMES: exclude anonymous volumes not referenced by any containers; local only
	// 1. build map of container-referenced volumes
	containerUsedVolumes := make(map[string]struct{})
	for _, c := range filteredContainers {
		if c.Mounts == nil {
			continue
		}
		for _, m := range c.Mounts {
			// volume mounts only
			if m.Type != "volume" || m.Driver != "local" {
				continue
			}
			containerUsedVolumes[m.Name] = struct{}{}
		}
	}
	// 2. filter volumes
	var filteredVolumes []dockertypes.Volume
	for _, v := range volumes {
		if v.Driver != "local" || v.Scope != "local" {
			continue
		}
		if v.Labels != nil {
			if _, ok := v.Labels["com.docker.volume.anonymous"]; ok {
				if _, ok := containerUsedVolumes[v.Name]; !ok {
					continue
				}
			}
		}
		filteredVolumes = append(filteredVolumes, v)
	}

	// FILTER IMAGES: either referenced by a container, OR (tagged AND not-pushed)
	// 1. build map of container-referenced images
	containerUsedImages := make(map[string]struct{})
	for _, c := range filteredContainers {
		containerUsedImages[c.ImageID] = struct{}{}
	}
	// 2. filter images
	var filteredImages []dockertypes.Image
	for _, i := range images {
		// exclude agent image
		if slices.Contains(i.RepoTags, migrationAgentImage) {
			continue
		}

		if _, ok := containerUsedImages[i.ID]; ok {
			filteredImages = append(filteredImages, i)
			continue
		}

		// not referenced by a container
		// check if tagged and not pushed
		if len(i.RepoTags) > 0 && len(i.RepoDigests) == 0 {
			filteredImages = append(filteredImages, i)
			continue
		}
	}

	// 4 workers parallel
	// TODO based on num_pcpus - 1
	errTracker := &errorTracker{}
	pool := pond.New(4, 1000, pond.PanicHandler(func(p any) {
		var err error
		if e, ok := p.(error); ok {
			err = e
		} else {
			err = fmt.Errorf("panic: %v", p)
		}
		logrus.Error(err)

		errTracker.mu.Lock()
		errTracker.errors = append(errTracker.errors, err)
		errTracker.mu.Unlock()
	}))
	defer pool.StopAndWait()
	preContainerGroup := pool.Group()

	// alright, filtering is done.
	// let's migrate in order

	// 1. images
	err = m.submitImages(preContainerGroup, filteredImages)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// 2. volumes
	// err = m.submitVolumes(preContainerGroup, filteredVolumes)
	// if err != nil {
	// 	return err
	// }
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// 3. networks
	err = m.submitNetworks(preContainerGroup, filteredNetworks)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// wait for container deps
	preContainerGroup.Wait()
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// 4. containers (depends on all above)
	err = m.submitContainers(preContainerGroup, filteredContainers)
	if err != nil {
		return err
	}
	err = errTracker.Check()
	if err != nil {
		return err
	}

	// end
	pool.StopAndWait()
	err = errTracker.Check()
	if err != nil {
		return err
	}

	return nil
}
