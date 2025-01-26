package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/orbstack/macvirt/scon/agent"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
)

func (m *ConManager) ImportContainerFromHostPath(newName, hostPath string) (_ *Container, retErr error) {
	file, err := securefs.Open(mounts.Virtiofs, hostPath)
	if err != nil {
		return nil, fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	// TODO
	oldName := "ubuntu"
	newC, _, err := m.beginCreate(&types.CreateRequest{
		// TODO: default to original name from export
		Name: newName,

		// TODO: get this from the tar instead of synthesizing this
		Image: types.ImageSpec{
			Distro:  "ubuntu",
			Version: "lunar",
			Arch:    "arm64",
			Variant: "default",
		},
		Config: types.MachineConfig{
			Isolated:        true,
			DefaultUsername: "dragon",
		},
	})
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			err2 := newC.deleteInternal()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container clone")
			}
		}
	}()

	err = newC.jobManager.Run(func(ctx context.Context) error {
		// create dir for bsdtar to extract into
		err = os.Mkdir(newC.rootfsDir, 0o755)
		if err != nil {
			return fmt.Errorf("create dir: %w", err)
		}

		cmd := exec.CommandContext(ctx, "bsdtar", "--zstd", "-C", newC.rootfsDir, "--xattrs", "--fflags", "-xf", "-")
		cmd.Stdin = file

		var stderrOutput bytes.Buffer
		cmd.Stderr = &stderrOutput

		err := cmd.Run()
		// prefer "context cancelled" over "signal: killed"
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil {
			return fmt.Errorf("extract archive: %w; output: %s", err, stderrOutput.String())
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("make rootfs: %w", err)
	}

	// update hostname
	err = agent.WriteHostnameFiles(newC.rootfsDir, oldName, newName, false /*runCommands*/)
	if err != nil {
		// soft fail for import
		logrus.WithError(err).WithField("container", newC.Name).Error("failed to update hostname")
	}

	// add to NFS
	// restoring the container doesn't call this if state=creating
	err = m.onRestoreContainer(newC)
	if err != nil {
		return nil, fmt.Errorf("call restore hook: %w", err)
	}

	newC.mu.Lock()
	defer newC.mu.Unlock()

	_, err = newC.transitionStateInternalLocked(types.ContainerStateStopped, true /*isInternal*/)
	if err != nil {
		return nil, fmt.Errorf("transition state: %w", err)
	}

	return newC, nil
}
