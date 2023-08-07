package agent

import (
	"path"
	"strings"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
)

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
