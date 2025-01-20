package dmigrate

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/orbstack/macvirt/scon/util"
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

	// translate network IDs
	m.mu.Lock()
	for _, n := range newCtrReq.NetworkingConfig.EndpointsConfig {
		n.NetworkID = m.networkIDMap[n.NetworkID]
	}
	m.mu.Unlock()

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
		return fmt.Errorf("create container: %w", err)
	}

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

func (m *Migrator) getContainerID(nameOrID string) (string, error) {
	if id, ok := m.srcNameToID[nameOrID]; ok {
		return id, nil
	}

	return "", fmt.Errorf("container '%s' not found", nameOrID)
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
		return nil, fmt.Errorf("add container mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.CgroupnsMode)
	if err != nil {
		return nil, fmt.Errorf("add container mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.IpcMode)
	if err != nil {
		return nil, fmt.Errorf("add container mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.PidMode)
	if err != nil {
		return nil, fmt.Errorf("add container mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.UTSMode)
	if err != nil {
		return nil, fmt.Errorf("add container mode dependency: %w", err)
	}
	deps, err = m.addContainerModeDependency(deps, ctr.HostConfig.UsernsMode)
	if err != nil {
		return nil, fmt.Errorf("add container mode dependency: %w", err)
	}

	for _, vol := range ctr.HostConfig.VolumesFrom {
		id, err := m.getContainerID(strings.SplitN(vol, ":", 2)[0])
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
