package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

func (c *Container) ExportToHostPath(hostPath string) (retErr error) {
	if c.builtin {
		return errors.New("cannot export builtin machine")
	}

	if c.Freezer() != nil {
		// should never happen, as only builtin containers have freezers
		return errors.New("cannot export machine with freezer")
	}

	hostUser, err := c.manager.host.GetUser()
	if err != nil {
		return fmt.Errorf("get host user: %w", err)
	}

	file, err := securefs.Create(mounts.Virtiofs, hostPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		// delete temp file if failed
		if retErr != nil {
			_ = securefs.Remove(mounts.Virtiofs, hostPath)
		}
	}()
	defer file.Close()

	err = c.holds.WithHold("export", func() error {
		// freeze container to get a consistent data snapshot
		err := c.Freeze()
		if err != nil && !errors.Is(err, ErrMachineNotRunning) {
			return fmt.Errorf("freeze: %w", err)
		}
		defer c.Unfreeze()

		configJson, err := json.Marshal(types.ExportedMachineV1{
			Version: types.ExportVersion,

			Record:     *c.toRecord(),
			ExportedAt: time.Now(),

			HostUID: uint32(hostUser.Uid),
			HostGID: uint32(hostUser.Gid),

			SourceFS: c.manager.fsOps.Name(),
		})
		if err != nil {
			return fmt.Errorf("marshal config: %w", err)
		}

		err = c.jobManager.Run(func(ctx context.Context) error {
			// include rootfs/ dir prefix in tar to allow flexibility for future extra data in machines data dirs
			cmd := exec.CommandContext(ctx, mounts.Starry, "tar", c.dataDir, string(configJson))
			cmd.Stdout = file

			var stderrOutput bytes.Buffer
			cmd.Stderr = &stderrOutput

			err := cmd.Run()
			// prefer "context cancelled" over "signal: killed"
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if err != nil {
				return fmt.Errorf("create archive: %w; output: %s", err, stderrOutput.String())
			}

			return nil
		})
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	return nil
}
