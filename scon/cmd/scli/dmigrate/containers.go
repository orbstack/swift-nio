package dmigrate

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockerclient"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func (m *Migrator) incContainerPauseRef(ctr *dockertypes.ContainerJSON) error {
	m.ctrPauseRefsMu.Lock()
	defer m.ctrPauseRefsMu.Unlock()

	m.ctrPauseRefs[ctr.ID]++
	newCount := m.ctrPauseRefs[ctr.ID]
	if newCount == 1 && ctr.State.Running {
		err := m.srcClient.Call("POST", "/containers/"+ctr.ID+"/pause", nil, nil)
		if err != nil {
			return fmt.Errorf("pause container: %w", err)
		}
	}

	return nil
}

func (m *Migrator) decContainerPauseRef(ctr *dockertypes.ContainerJSON) error {
	m.ctrPauseRefsMu.Lock()
	defer m.ctrPauseRefsMu.Unlock()

	m.ctrPauseRefs[ctr.ID]--
	newCount := m.ctrPauseRefs[ctr.ID]
	if newCount == 0 && ctr.State.Running {
		err := m.srcClient.Call("POST", "/containers/"+ctr.ID+"/unpause", nil, nil)
		if err != nil {
			logrus.Warnf("unpause container: %v", err)
		}
	}

	return nil
}

func (m *Migrator) translateModeContainerId(mode *string) error {
	if strings.HasPrefix(*mode, "container:") {
		id := strings.TrimPrefix(*mode, "container:")

		m.mu.Lock()
		mappedID, ok := m.containerIDMap[id]
		m.mu.Unlock()
		if !ok {
			return fmt.Errorf("map container id: %s", id)
		}

		*mode = "container:" + mappedID
	}

	return nil
}

func (m *Migrator) migrateOneContainer(ctr *dockertypes.ContainerJSON, userName string) error {
	logrus.Infof("Migrating container %s", userName)

	// [src] fetch full info
	fullCtr, err := m.srcClient.InspectContainer(ctr.ID)
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

	// translate leaked /host_mnt/* paths
	if newCtrReq.HostConfig != nil {
		for _, vol := range newCtrReq.HostConfig.Mounts {
			if strings.HasPrefix(vol.Source, "/host_mnt/") {
				vol.Source = "/" + strings.TrimPrefix(vol.Source, "/host_mnt/")
			}
		}
	}

	// can't specify Hostname when NetworkMode is set
	if strings.HasPrefix(newCtrReq.HostConfig.NetworkMode, "container:") {
		newCtrReq.ContainerConfig.Hostname = ""
	}

	// translate network IDs
	m.mu.Lock()
	for _, n := range newCtrReq.NetworkingConfig.EndpointsConfig {
		n.NetworkID = m.networkIDMap[n.NetworkID]
	}
	m.mu.Unlock()

	// translate container IDs in mode fields
	err = m.translateModeContainerId(&newCtrReq.HostConfig.NetworkMode)
	if err != nil {
		return fmt.Errorf("translate network mode container id: %w", err)
	}
	err = m.translateModeContainerId(&newCtrReq.HostConfig.CgroupnsMode)
	if err != nil {
		return fmt.Errorf("translate cgroupns mode container id: %w", err)
	}
	err = m.translateModeContainerId(&newCtrReq.HostConfig.IpcMode)
	if err != nil {
		return fmt.Errorf("translate ipc mode container id: %w", err)
	}
	err = m.translateModeContainerId(&newCtrReq.HostConfig.PidMode)
	if err != nil {
		return fmt.Errorf("translate pid mode container id: %w", err)
	}
	err = m.translateModeContainerId(&newCtrReq.HostConfig.UTSMode)
	if err != nil {
		return fmt.Errorf("translate uts mode container id: %w", err)
	}
	err = m.translateModeContainerId(&newCtrReq.HostConfig.UsernsMode)
	if err != nil {
		return fmt.Errorf("translate userns mode container id: %w", err)
	}

	// translate container id in volumesFrom
	for i, vol := range newCtrReq.HostConfig.VolumesFrom {
		volParts := strings.SplitN(vol, ":", 2)
		id, err := m.getSrcContainerID(volParts[0])
		if err != nil {
			return fmt.Errorf("get container id: %w", err)
		}

		m.mu.Lock()
		mappedID, ok := m.containerIDMap[id]
		m.mu.Unlock()
		if !ok {
			return fmt.Errorf("map container id: %s", id)
		}

		newCtrReq.HostConfig.VolumesFrom[i] = mappedID
		if len(volParts) > 1 {
			newCtrReq.HostConfig.VolumesFrom[i] += ":" + volParts[1]
		}
	}

	// connect anonymous volumes
	// these are the volumes in the mountpoints list (ctr.Mounts) but not binds (ctr.HostConfig.Binds) or mounts (ctr.HostConfig.Mounts)
	// these are also volumes in ctr.HostConfig.Mounts without a source?
	mountpointsByDestination := make(map[string]dockertypes.MountPoint, len(fullCtr.Mounts))
	for _, vol := range fullCtr.Mounts {
		mountpointsByDestination[vol.Destination] = vol
	}
	knownVolumes := make([]string, 0, len(newCtrReq.HostConfig.Mounts)+len(newCtrReq.HostConfig.Binds))
	for i, vol := range newCtrReq.HostConfig.Mounts {
		if vol.Type != "volume" {
			continue
		}

		// if it's a volume type without a source, it's anonymous, so we should patch it to use the corresponding anonymous volume from the mountpoints list
		if vol.Source == "" {
			mountpoint, ok := mountpointsByDestination[vol.Target]
			if !ok {
				return fmt.Errorf("find corresponding mountpoint for target `%s`", vol.Target)
			}

			if mountpoint.Type != "volume" {
				return fmt.Errorf("mountpoint for target `%s` is not a volume, but has a corresponding container mount", vol.Target)
			}

			newCtrReq.HostConfig.Mounts[i].Source = mountpoint.Name
		}

		knownVolumes = append(knownVolumes, vol.Source)
	}
	for _, vol := range newCtrReq.HostConfig.Binds {
		volParts := strings.SplitN(vol, ":", 2)

		// skip if bind mount
		// paths have to be absolute for a bind mount so if this starts with /, it's a bind mount
		if strings.HasPrefix(volParts[0], "/") {
			continue
		}

		knownVolumes = append(knownVolumes, volParts[0])
	}
	for _, vol := range fullCtr.Mounts {
		if vol.Type != "volume" {
			continue
		}

		// these volumes are all already handled
		if slices.Contains(knownVolumes, vol.Name) {
			continue
		}

		newCtrReq.HostConfig.Mounts = append(newCtrReq.HostConfig.Mounts, dockertypes.ContainerMount{
			Type:   "volume",
			Source: vol.Name,
			Target: vol.Destination,
		})
	}

	// can only connect 1 endpoint at creation time
	// if more, save them for later
	extraEndpoints := make(map[string]*dockertypes.NetworkEndpointSettings)
	if len(newCtrReq.NetworkingConfig.EndpointsConfig) > 1 {
		isFirst := true
		for k, v := range newCtrReq.NetworkingConfig.EndpointsConfig {
			if isFirst {
				isFirst = false
				continue
			}

			extraEndpoints[k] = v
			delete(newCtrReq.NetworkingConfig.EndpointsConfig, k)
		}
	}

	// if no architecture set, use docker default image selection logic
	// this fixes amd64 migration when no explicit arch was specified in the source container
	platform := fullCtr.Platform
	if platform == "linux" {
		platform = ""
	}

	err = m.destClient.Call("POST", "/containers/create?name="+url.QueryEscape(fullCtr.Name)+"&platform="+url.QueryEscape(platform), newCtrReq, &newCtrResp)
	if err != nil {
		var apiErr *dockerclient.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatus == 409 {
			// container already exists, we'll grab its id to add to the containerID map
			newFullCtr, err := m.destClient.InspectContainer(fullCtr.Name)
			if err != nil {
				return fmt.Errorf("get existing dest container: %w", err)
			}

			m.mu.Lock()
			m.containerIDMap[ctr.ID] = newFullCtr.ID
			m.mu.Unlock()
		}

		return fmt.Errorf("create container: %w", err)
	}

	m.mu.Lock()
	m.containerIDMap[ctr.ID] = newCtrResp.ID
	m.mu.Unlock()

	// [dest] connect extra net endpoints
	for k, v := range extraEndpoints {
		err = m.destClient.Call("POST", "/networks/"+k+"/connect", dockertypes.NetworkConnectRequest{
			Container:      newCtrResp.ID,
			EndpointConfig: v,
		}, nil)
		if err != nil {
			return fmt.Errorf("connect net endpoint: %w", err)
		}
	}

	// [dest] get new full info
	var newFullCtr dockertypes.ContainerJSON
	err = m.destClient.Call("GET", "/containers/"+newCtrResp.ID+"/json", nil, &newFullCtr)
	if err != nil {
		return fmt.Errorf("get dest container: %w", err)
	}

	// [src] pause container
	err = m.incContainerPauseRef(ctr)
	if err != nil {
		return fmt.Errorf("inc container pause ref: %w", err)
	}
	defer m.decContainerPauseRef(ctr)

	// if not overlay2, then we're done, can't transfer
	if fullCtr.GraphDriver.Name != "overlay2" {
		logrus.Warnf("container %s: graph driver %s not supported", ctr.ID, fullCtr.GraphDriver.Name)
		return nil
	}

	// [src] sync the upper dir
	srcUpper := fullCtr.GraphDriver.Data["UpperDir"]
	destUpper := newFullCtr.GraphDriver.Data["UpperDir"]
	err = m.syncDirs(m.srcClient, []string{srcUpper}, m.destClient, destUpper)
	if err != nil {
		return fmt.Errorf("sync upper dir: %w", err)
	}

	return nil
}

func (m *Migrator) getSrcContainerID(nameOrID string) (string, error) {
	if id, ok := m.srcNameToID[nameOrID]; ok {
		return id, nil
	} else {
		return "", fmt.Errorf("container '%s' not found", nameOrID)
	}
}

func (m *Migrator) addContainerModeDependency(deps []string, field string) ([]string, error) {
	if strings.HasPrefix(field, "container:") {
		id := strings.TrimPrefix(field, "container:")
		deps = append(deps, id)
	}

	return deps, nil
}

func (m *Migrator) getContainerDependencies(ctr *dockertypes.ContainerJSON) ([]string, error) {
	deps := []string{}

	var err error
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.NetworkMode)
	if err != nil {
		return nil, fmt.Errorf("add container network mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.CgroupnsMode)
	if err != nil {
		return nil, fmt.Errorf("add container cgroupns mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.IpcMode)
	if err != nil {
		return nil, fmt.Errorf("add container ipc mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.PidMode)
	if err != nil {
		return nil, fmt.Errorf("add container pid mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.UTSMode)
	if err != nil {
		return nil, fmt.Errorf("add container uts mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.UsernsMode)
	if err != nil {
		return nil, fmt.Errorf("add container userns mode dependency: %w", err)
	}

	for _, vol := range ctr.HostConfig.VolumesFrom {
		id, err := m.getSrcContainerID(strings.SplitN(vol, ":", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("get container id: %w", err)
		}

		deps = append(deps, id)
	}

	return deps, nil
}

func (m *Migrator) addOneContainerMigration(runner *util.DependentTaskRunner[string], ctr *dockertypes.ContainerJSON) {
	userName := ctr.Name

	deps, err := m.getContainerDependencies(ctr)
	if err != nil {
		logrus.Errorf("get container dependencies: %v", err)
	}

	logrus.WithField("container", userName).Debug("Submitting container")
	runner.AddTask(ctr.ID, func() error {
		defer m.finishOneEntity()

		err := m.migrateOneContainer(ctr, userName)
		if err != nil {
			logrus.Errorf("container %s: %v", userName, err)
		}

		return nil
	}, deps)
}

func (m *Migrator) submitOneContainerMigration(runner *util.DependentTaskRunner[string], id string) error {
	runner.Run(id)
	return nil
}
