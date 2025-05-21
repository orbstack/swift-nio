package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/orbstack/macvirt/vmgr/dockertypes"
)

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

func (a *AgentServer) DockerImportVolumeFromHostPath(args types.InternalDockerImportVolumeFromHostPathRequest, reply *None) (retErr error) {
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
	defer func() {
		if retErr != nil {
			_ = a.docker.realClient.DeleteVolume(newVol.Name)
		}
	}()

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
