package dmigrate

import (
	"fmt"
	"net/url"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func (m *Migrator) incContainerPauseRef(ctr *dockertypes.ContainerSummary) error {
	m.ctrPauseRefsMu.Lock()
	defer m.ctrPauseRefsMu.Unlock()

	m.ctrPauseRefs[ctr.ID]++
	newCount := m.ctrPauseRefs[ctr.ID]
	if newCount == 1 && ctr.State == "running" {
		err := m.srcClient.Call("POST", "/containers/"+ctr.ID+"/pause", nil, nil)
		if err != nil {
			return fmt.Errorf("pause container: %w", err)
		}
	}

	return nil
}

func (m *Migrator) decContainerPauseRef(ctr *dockertypes.ContainerSummary) error {
	m.ctrPauseRefsMu.Lock()
	defer m.ctrPauseRefsMu.Unlock()

	m.ctrPauseRefs[ctr.ID]--
	newCount := m.ctrPauseRefs[ctr.ID]
	if newCount == 0 && ctr.State == "running" {
		err := m.srcClient.Call("POST", "/containers/"+ctr.ID+"/unpause", nil, nil)
		if err != nil {
			logrus.Warnf("unpause container: %v", err)
		}
	}

	return nil
}

func (m *Migrator) migrateOneContainer(ctr *dockertypes.ContainerSummary, userName string) error {
	logrus.Infof("Migrating container %s", userName)

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

func (m *Migrator) submitContainers(group *pond.TaskGroup, ctrs []*dockertypes.ContainerSummary) error {
	for _, ctr := range ctrs {
		var userName string
		if len(ctr.Names) > 0 {
			userName = ctr.Names[0]
		} else {
			userName = ctr.ID
		}

		ctr := ctr
		group.Submit(func() {
			defer m.finishOneEntity(&entitySpec{containerID: ctr.ID})

			err := m.migrateOneContainer(ctr, userName)
			if err != nil {
				panic(fmt.Errorf("container %s: %w", userName, err))
			}
		})
	}

	return nil
}
