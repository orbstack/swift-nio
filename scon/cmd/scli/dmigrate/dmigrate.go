package dmigrate

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
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

func (m *Migrator) migrateImages(images []dockertypes.Image) error {
	// one by one
	migrateOneImage := func(idx int, img dockertypes.Image, userImageName string) error {
		// open export conn
		logrus.Infof("Exporting image %s (%d/%d)", userImageName, idx+1, len(images))
		exportConn, err := m.srcClient.Stream("GET", "/images/"+img.ID+"/get")
		if err != nil {
			return fmt.Errorf("open export conn: %w", err)
		}
		defer exportConn.Close()

		// open import conn and copy
		logrus.Infof("Importing image %s (%d/%d)", userImageName, idx+1, len(images))
		err = m.destClient.StreamWrite("POST", "/images/load", exportConn)
		if err != nil {
			return fmt.Errorf("open import conn: %w", err)
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

		err := migrateOneImage(idx, img, userImageName)
		if err != nil {
			return fmt.Errorf("image %s: %w", userImageName, err)
		}
	}

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
