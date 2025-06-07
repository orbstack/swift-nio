package agent

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

var errContainerExited = errors.New("container exited")

func openPidProcDirAndSend(pid int, fdx *Fdx) (uint64, error) {
	// this is racy, but it's the same amount of racy as opening pidfd from pid
	procFd, err := unix.Open("/proc/"+strconv.Itoa(pid), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) {
			return 0, errContainerExited
		} else {
			return 0, fmt.Errorf("open proc net dir: %w", err)
		}
	}
	defer unix.Close(procFd)

	seq, err := fdx.SendFdInt(procFd)
	if err != nil {
		return 0, fmt.Errorf("send fd: %w", err)
	}
	return seq, nil
}

func (d *DockerAgent) refreshContainers() error {
	// no mu needed: FuncDebounce has mutex

	// only includes running
	var newContainers []dockertypes.ContainerSummaryMin
	err := d.realClient.Call("GET", "/containers/json", nil, &newContainers)
	if err != nil {
		return err
	}

	// diff
	d.lastContainersMu.Lock()
	removed, added := util.DiffSlicesKey(d.lastContainers, newContainers)
	d.lastContainers = newContainers
	d.lastContainersMu.Unlock()

	// remove first
	// must remove before adding in case of recreate with same name within debounce period
	for _, c := range removed {
		err = d.onContainerStop(c)
		if err != nil {
			logrus.WithError(err).Error("failed to remove container")
		}
	}

	// then add
	// fdx seqs can't be 0, so zero = missing
	addedMeta := make([]sgtypes.AddedContainerMeta, len(added))
	for i, c := range added {
		fullCtr, err := d.realClient.InspectContainer(c.ID)
		if err != nil {
			logrus.WithError(err).WithField("cid", c.ID).Warn("failed to inspect container")
			continue
		}

		err = d.onContainerStart(c)
		if err != nil {
			logrus.WithError(err).Error("failed to add container")
		}

		// open /proc/pid and send over fdx
		// do it one at a time to allow for failures, and b/c 16-fd limit
		addedMeta[i].ProcDirFdxSeq, err = openPidProcDirAndSend(fullCtr.State.Pid, d.agent.fdx)
		if err != nil && !errors.Is(err, errContainerExited) {
			// container exited is normal - just don't mount
			logrus.WithError(err).Error("failed to send proc dir")
		}

		var cgroupPath string
		if fullCtr.HostConfig.CgroupParent != "" {
			cgroupPath = fullCtr.HostConfig.CgroupParent + "/" + fullCtr.ID
		} else if fullCtr.HostConfig.Cgroup != "" {
			cgroupPath = fullCtr.HostConfig.Cgroup
		} else {
			cgroupPath = "/docker/" + fullCtr.ID
		}
		addedMeta[i].CgroupPath = cgroupPath
		addedMeta[i].Pid = fullCtr.State.Pid
	}

	// tell scon
	err = d.scon.OnDockerContainersChanged(sgtypes.ContainersDiff{
		Diff: sgtypes.Diff[dockertypes.ContainerSummaryMin]{
			Added:   added,
			Removed: removed,
		},
		AddedContainerMeta: addedMeta,
	})
	if err != nil {
		logrus.WithError(err).Error("failed to update scon containers")
	}

	// we could have new bridge ports now
	err = d.refreshFlowtable()
	if err != nil {
		logrus.WithError(err).Error("failed to refresh FT")
	}

	return nil
}

func translateDockerPathToMac(p string) string {
	p = path.Clean(p)

	// need to resolve symlinks for /var/folders, /tmp, etc. to work correctly (extra symlinked dirs in docker machine)
	newPath, err := filepath.EvalSymlinks(p)
	if err == nil {
		p = newPath
	} else {
		logrus.WithError(err).WithField("path", p).Warn("failed to resolve symlink")
	}

	// if under /mnt/mac, translate
	if p == mounts.Virtiofs || strings.HasPrefix(p, mounts.Virtiofs+"/") {
		return strings.TrimPrefix(p, mounts.Virtiofs)
	}

	// if linked, do nothing
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
			err := d.realClient.Call("GET", "/volumes/"+m.Name, nil, &volInfo)
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
		newPath := translateDockerPathToMac(path)
		if newPath == "" {
			logrus.WithField("path", path).Debug("ignoring bind mount")
			continue
		}

		err := d.host.AddFsnotifyRef(newPath)
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

func (a *AgentServer) DockerOnContainerPreStart(cid string, reply *None) error {
	// pretend that container was removed from cache
	a.docker.lastContainersMu.Lock()
	a.docker.lastContainers = slices.DeleteFunc(a.docker.lastContainers, func(ctr dockertypes.ContainerSummaryMin) bool {
		return ctr.ID == cid
	})
	a.docker.lastContainersMu.Unlock()

	// inject certs
	err := a.docker.certInjector.addToContainer(cid)
	if err != nil {
		return err
	}

	return nil
}
