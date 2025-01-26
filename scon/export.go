package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"

	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
)

func (c *Container) ExportToHostPath(hostPath string) error {
	if c.builtin {
		return errors.New("cannot export builtin machine")
	}

	if c.Freezer() != nil {
		// should never happen, as only builtin containers have freezers
		return errors.New("cannot export machine with freezer")
	}

	file, err := securefs.Create(mounts.Virtiofs, hostPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer file.Close()

	err = c.holds.WithHold("export", func() error {
		// freeze container to get a consistent data snapshot
		err := c.Freeze()
		if err != nil && !errors.Is(err, ErrMachineNotRunning) {
			return fmt.Errorf("freeze: %w", err)
		}
		defer c.Unfreeze()

		err = c.jobManager.Run(func(ctx context.Context) error {
			cmd := exec.CommandContext(ctx, mounts.Starry, "tar", c.rootfsDir)
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
