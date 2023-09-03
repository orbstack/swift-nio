package agent

import (
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
)

func filterVolumes(vols []*dockertypes.Volume) []*dockertypes.Volume {
	var newVols []*dockertypes.Volume
	for _, v := range vols {
		// we only deal with local, and don't take options (e.g. weird binds)
		if v.Driver != "local" || v.Scope != "local" || len(v.Options) > 0 {
			continue
		}

		newVols = append(newVols, v)
	}
	return newVols
}

func (d *DockerAgent) refreshVolumes() error {
	// no mu needed: FuncDebounce has mutex

	newVolumes, err := d.client.ListVolumes()
	if err != nil {
		return err
	}

	// filter to only local volumes
	newVolumes = filterVolumes(newVolumes)

	// diff
	removed, added := util.DiffSlicesKey(d.lastVolumes, newVolumes)

	// tell scon
	err = d.scon.OnDockerVolumesChanged(sgtypes.Diff[*dockertypes.Volume]{
		Removed: removed,
		Added:   added,
	})
	if err != nil {
		logrus.WithError(err).Error("failed to update scon volumes")
	}

	d.lastVolumes = newVolumes
	return nil
}
