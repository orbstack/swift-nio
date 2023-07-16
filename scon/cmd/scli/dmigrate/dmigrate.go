package dmigrate

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

const (
	// TODO: build custom image (amd64 and arm64) with all this included
	//cmdImage = "ghcr.io/orbstack/dmigrate-helper"
	cmdImage      = "alpine:20230329"
	registryImage = "registry:2"
)

type Migrator struct {
	srcClient  *dockerclient.Client
	destClient *dockerclient.Client
}

type MigrateParams struct {
	IncludeContainers bool
	IncludeVolumes    bool
	IncludeImages     bool
	/* networks are implicit by containers */
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

func (m *Migrator) runCommandAs(client *dockerclient.Client, command ...string) error {
	// create --rm container
	cid, err := m.createAndStartContainer(client, &dockertypes.ContainerCreateRequest{
		Image: cmdImage,
		Cmd:   command,
		HostConfig: &dockertypes.ContainerHostConfig{
			Privileged:  true,
			AutoRemove:  true,
			NetworkMode: "host",
		},
	})
	if err != nil {
		return fmt.Errorf("create and start container: %w", err)
	}

	// wait for container to exit
	err = client.Call("POST", "/containers/"+cid+"/wait", nil, nil)
	if err != nil {
		// TODO does not exist = already exited
		return fmt.Errorf("wait for container: %w", err)
	}

	return nil
}

func (m *Migrator) migrateImages(images []dockertypes.Image) error {
	// find a free localhost TCP port on Mac
	logrus.Info("finding free TCP port on localhost")
	localRegistryPort, err := findFreeTCPPort()
	if err != nil {
		return fmt.Errorf("find free TCP port: %w", err)
	}
	logrus.Infof("found free TCP port on localhost: %d", localRegistryPort)

	// [dest] start local registry server
	logrus.Info("starting local registry server")
	registryCID, err := m.createAndStartContainer(m.destClient, &dockertypes.ContainerCreateRequest{
		Image: registryImage,
		HostConfig: &dockertypes.ContainerHostConfig{
			AutoRemove: true,
			PortBindings: map[string][]dockertypes.PortBinding{
				"5000/tcp": {
					{
						HostIP:   "127.0.0.1",
						HostPort: fmt.Sprintf("%d", localRegistryPort),
					},
				},
			},
		},
	})
	//TODO prevent race- wait for registry to be ready
	if err != nil {
		return fmt.Errorf("create registry container: %w", err)
	}
	// defer: [dest] kill&delete local registry server
	defer func() {
		logrus.Info("killing local registry server")
		err = m.destClient.Call("POST", "/containers/"+registryCID+"/kill", nil, nil)
		if err != nil {
			logrus.WithError(err).Warn("[cleanup] failed to kill local registry server")
		}
		// we have auto-remove
	}()

	// [src] add iptables forward
	// avoid having to add insecure-registries=host.docker.internal and restart dockerd
	logrus.Info("adding iptables forward on src")
	err = m.runCommandAs(m.srcClient, "sh", "-c", fmt.Sprintf(`
		set -eufo pipefail
		apk add --no-cache iptables
		sysctl net.ipv4.conf.eth0.route_localnet=1
		iptables -t nat -A OUTPUT -o lo -p tcp -m tcp --dport %d -j DNAT --to-destination $(getent hosts host.docker.internal | cut -d' ' -f1)
		iptables -t nat -A POSTROUTING -o eth0 -m addrtype --src-type LOCAL --dst-type UNICAST -j MASQUERADE
	`, localRegistryPort))
	if err != nil {
		return fmt.Errorf("add src iptables forward: %w", err)
	}
	// defer: [src] remove iptables forward
	defer func() {
		logrus.Info("removing iptables forward on src")
		err = m.runCommandAs(m.srcClient, "sh", "-c", fmt.Sprintf(`
			set -eufo pipefail
			apk add --no-cache iptables
			sysctl net.ipv4.conf.eth0.route_localnet=0
			iptables -t nat -D OUTPUT -o lo -p tcp -m tcp --dport %d -j DNAT --to-destination $(getent hosts host.docker.internal | cut -d' ' -f1)
			iptables -t nat -D POSTROUTING -o eth0 -m addrtype --src-type LOCAL --dst-type UNICAST -j MASQUERADE
		`, localRegistryPort))
		if err != nil {
			logrus.WithError(err).Warn("[cleanup] failed to remove iptables forward")
		}
	}()

	// one by one:
	// 1. [src] add temp tag
	// 2. [src] push image
	// 3. [dest] pull image
	// 4. [dest] add real tag
	// 5. [dest] remove temp tag
	// TODO 6. [dest] delete image from registry
	// 7. [deferred] [src] remove temp tag
	// make sure we use 127.0.0.1. only ipv4 localnet can be redirected
	imgTagPrefix := fmt.Sprintf("127.0.0.1:%d/orbdmigrate", localRegistryPort)
	migrateOneImage := func(idx int, img dockertypes.Image, userImageName string) error {
		// [src] add temp tag
		logrus.Infof("adding temp tag to image %s", userImageName)
		tempTag := fmt.Sprintf("%s%d", imgTagPrefix, idx)
		err = m.srcClient.Call("POST", "/images/"+img.ID+"/tag?repo="+url.QueryEscape(tempTag), nil, nil)
		if err != nil {
			return fmt.Errorf("add temp tag: %w", err)
		}
		// defer: [src] remove temp tag
		defer func() {
			logrus.Infof("removing temp tag from image %s", userImageName)
			err = m.srcClient.Call("DELETE", "/images/"+url.PathEscape(tempTag), nil, nil)
			if err != nil {
				logrus.WithError(err).Warn("[cleanup] failed to remove temp tag")
			}
		}()

		// [src] push image
		logrus.Infof("pushing image %s", userImageName)
		err = m.srcClient.Call("POST", "/images/"+url.PathEscape(tempTag)+"/push", nil, nil)
		if err != nil {
			return fmt.Errorf("push image: %w", err)
		}

		// [dest] pull image
		logrus.Infof("pulling image %s", userImageName)
		err = m.destClient.Call("POST", "/images/create?fromImage="+url.QueryEscape(tempTag), nil, nil)
		if err != nil {
			return fmt.Errorf("pull image: %w", err)
		}

		// [dest] add real tags
		logrus.Infof("adding real tags to image %s", userImageName)
		for _, repoTag := range img.RepoTags {
			repoPart, tagPart := splitRepoTag(repoTag)
			err = m.destClient.Call("POST", "/images/"+img.ID+"/tag?repo="+url.QueryEscape(repoPart)+"&tag="+url.QueryEscape(tagPart), nil, nil)
			if err != nil {
				return fmt.Errorf("add real tag: %w", err)
			}
		}
		// [dest] remove temp tag
		logrus.Infof("removing temp tag from image %s", userImageName)
		err = m.destClient.Call("DELETE", "/images/"+url.PathEscape(tempTag), nil, nil)
		if err != nil {
			return fmt.Errorf("remove temp tag: %w", err)
		}

		return nil
	}
	for idx, img := range images {
		var userImageName string
		if len(img.RepoTags) > 0 {
			userImageName = img.RepoTags[0]
		} else {
			userImageName = img.ID
		}

		err = migrateOneImage(idx, img, userImageName)
		if err != nil {
			return fmt.Errorf("image %s: %w", userImageName, err)
		}
	}

	// deferred:
	// [dest] stop&delete local registry server
	// [src] remove iptables forward
	return nil
}

func (m *Migrator) MigrateNetworks() error {
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

	// FILTER NETWORKS: must be Scope="local" Driver="bridge" and referenced by container
	// 1. build map of container-referenced networks
	containerUsedNets := make(map[string]struct{})
	if params.IncludeContainers {
		for _, c := range containers {
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
	if params.IncludeContainers {
		for _, c := range containers {
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
	for _, c := range containers {
		containerUsedImages[c.ImageID] = struct{}{}
	}
	// 2. filter images
	var filteredImages []dockertypes.Image
	for _, i := range images {
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

	// FILTER CONTAINERS: for now, include all, we don't know.
	// TODO: exclude Compose ones where project files still exist?
	filteredContainers := containers

	// alright, filtering is done.
	// let's migrate in order

	// 1. images
	err = m.migrateImages(filteredImages)
	if err != nil {
		return err
	}

	// TODO 2. networks

	// TODO 3. volumes

	// TODO 4. containers (depends on all above)
	_ = filteredContainers

	return nil
}
