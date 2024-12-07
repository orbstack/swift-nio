package agent

import (
	"strconv"
	"strings"

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

	newVolumes, err := d.realClient.ListVolumes()
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

// busybox du is 2-5x faster than docker system df
// 25 sec -> 8 sec for 2.5M files totaling 120 GB. burns less CPU
func (a *AgentServer) DockerFastDf(_ None, reply *dockertypes.SystemDf) error {
	vols, err := a.docker.realClient.ListVolumes()
	if err != nil {
		return err
	}

	for _, v := range vols {
		if v.Driver != "local" || v.Scope != "local" || len(v.Options) > 0 {
			continue
		}

		// run one df command per volume
		// TODO: fix race on concurrent symlink modification (not a security-critical context)
		// TODO: change cwd to improve perf (less path resolution)
		out, err := util.RunWithOutput("du", "-s", v.Mountpoint)
		if err != nil {
			if strings.Contains(err.Error(), ": No such file or directory") {
				// race: volume was removed
				continue
			} else {
				return err
			}
		}

		// parse output
		for _, line := range strings.Split(out, "\n") {
			// format: size (in 1KiB units) \t path
			sz, path, ok := strings.Cut(line, "\t")
			if !ok {
				continue
			}

			szKib, err := strconv.ParseInt(sz, 10, 64)
			if err != nil {
				logrus.WithError(err).Error("failed to parse df output")
				continue
			}

			if path == v.Mountpoint {
				v.UsageData = &dockertypes.VolumeUsageData{
					Size: szKib * 1024,
				}
			}
		}
	}

	*reply = dockertypes.SystemDf{
		Volumes: vols,
	}

	return nil
}
