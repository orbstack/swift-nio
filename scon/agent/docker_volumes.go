package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/sgclient/sgtypes"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/scon/util"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
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

	duArgs := []string{mounts.Starry, "du"}
	for _, v := range vols {
		if v.Driver != "local" || v.Scope != "local" || len(v.Options) > 0 {
			continue
		}

		duArgs = append(duArgs, v.Mountpoint)
	}

	// only run du if there are eligible volumes
	if len(duArgs) > 2 {
		// starry du is safe against symlink races and deletion/ENOENT races, even for the root dir of a volume
		out, err := util.RunWithOutput(duArgs...)
		if err != nil {
			return err
		}

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

			// update corresponding volume with the new usage data
			for _, v := range vols {
				if path == v.Mountpoint {
					v.UsageData = &dockertypes.VolumeUsageData{
						Size: szKib * 1024,
					}
					break
				}
			}
		}
	}

	*reply = dockertypes.SystemDf{
		Volumes: vols,
	}

	return nil
}

func (a *AgentServer) DockerExportVolumeToHostPath(args types.InternalDockerExportVolumeToHostPathRequest, reply *None) (retErr error) {
	volume, err := a.docker.realClient.GetVolume(args.VolumeID)
	if err != nil {
		return fmt.Errorf("get volume: %w", err)
	}

	if volume.Driver != "local" || volume.Scope != "local" || volume.Mountpoint == "" {
		return errors.New("can't export non-local volume")
	}
	// this means bind mount or some other non-local mount
	if _, ok := volume.Options["device"]; ok {
		return errors.New("can't export volume with custom mount options")
	}

	file, err := securefs.Create(mounts.Virtiofs, args.HostPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		// delete temp file if failed
		if retErr != nil {
			_ = securefs.Remove(mounts.Virtiofs, args.HostPath)
		}
	}()
	defer file.Close()

	// TODO: put original name + options + labels in zstd skippable frame
	// we don't want user-facing files in these tarballs in case they're being used as raw content exports

	// include rootfs/ dir prefix in tar to allow flexibility for future extra data in machines data dirs
	cmd := exec.Command(mounts.Starry, "tar", volume.Mountpoint)
	cmd.Stdout = file

	var stderrOutput bytes.Buffer
	cmd.Stderr = &stderrOutput

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("create archive: %w; output: %s", err, stderrOutput.String())
	}

	return nil
}

func (a *AgentServer) DockerImportVolumeFromHostPath(args types.InternalDockerImportVolumeFromHostPathRequest, reply *None) error {
	// open the file first to make sure it exists
	file, err := securefs.Open(mounts.Virtiofs, args.HostPath)
	if err != nil {
		return fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// attempt to create the new volume by name
	newVol, err := a.docker.realClient.CreateVolume(dockertypes.VolumeCreateRequest{
		Name: args.NewName,
	})
	if err != nil {
		return fmt.Errorf("create volume: %w", err)
	}

	// extract the tarball into the new volume
	// for compression, bsdtar has "--options zstd:threads=N", but there's no zstdmt for decompression
	cmd := exec.Command("bsdtar", "--zstd", "-C", newVol.Mountpoint, "--xattrs", "--fflags", "-xf", "-")
	cmd.Stdin = file

	var stderrOutput bytes.Buffer
	cmd.Stderr = &stderrOutput

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("extract archive: %w; output: %s", err, stderrOutput.String())
	}

	return nil
}
