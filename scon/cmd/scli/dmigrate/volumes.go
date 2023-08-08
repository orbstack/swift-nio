package dmigrate

import (
	"fmt"

	"github.com/alitto/pond"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func (m *Migrator) migrateOneVolume(vol dockertypes.Volume, depContainers []*dockertypes.ContainerSummary) error {
	// create volume on dest
	logrus.Infof("Migrating volume %s", vol.Name)
	var newVol dockertypes.Volume
	err := m.destClient.Call("POST", "/volumes/create", dockertypes.VolumeCreateRequest{
		Name:       vol.Name,
		DriverOpts: vol.Options,
		Labels:     vol.Labels,
	}, &newVol)
	if err != nil {
		return fmt.Errorf("create volume: %w", err)
	}

	// if it's a bind mount or any other type of mount that's not simple local, then we're done
	if _, ok := vol.Options["device"]; ok {
		return nil
	}

	// freeze containers
	for _, ctr := range depContainers {
		err := m.incContainerPauseRef(ctr)
		if err != nil {
			return fmt.Errorf("inc container pause ref: %w", err)
		}
		defer m.decContainerPauseRef(ctr)
	}

	// sync data dirs
	err = m.syncDirs(m.srcClient, []string{vol.Mountpoint}, m.destClient, newVol.Mountpoint)
	if err != nil {
		return fmt.Errorf("sync dirs: %w", err)
	}

	return nil
}

func (m *Migrator) submitVolumes(group *pond.TaskGroup, volumes []dockertypes.Volume, containerUsedVolumes map[string][]*dockertypes.ContainerSummary) error {
	for _, vol := range volumes {
		vol := vol
		logrus.WithField("volume", vol.Name).Debug("submitting volume")
		group.Submit(func() {
			defer m.finishOneEntity(&entitySpec{volumeName: vol.Name})

			err := m.migrateOneVolume(vol, containerUsedVolumes[vol.Name])
			if err != nil {
				panic(fmt.Errorf("volume %s: %w", vol.Name, err))
			}
		})
	}

	return nil
}
