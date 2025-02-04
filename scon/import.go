package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/klauspost/compress/zstd"
	"github.com/orbstack/macvirt/scon/securefs"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/mounts"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	// '_' should sort first in tar
	exportedConfigDir  = "_orbstack"
	exportedConfigPath = exportedConfigDir + "/v1/config.json"

	// scan a few more entries in case bsdtar adds directories
	exportedConfigScanEntries = 10
)

func readConfigFromExport(file *os.File) (*types.ExportedMachineV1, error) {
	// decompress zstd
	decomp, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, fmt.Errorf("create zstd reader: %w", err)
	}
	defer decomp.Close()

	// read tar entry
	tarReader := tar.NewReader(decomp)
	for i := 0; i < exportedConfigScanEntries; i++ {
		header, err := tarReader.Next()
		if err != nil {
			return nil, fmt.Errorf("read tar entry: %w", err)
		}
		if header.Name == exportedConfigPath || header.Name == "./"+exportedConfigPath {
			// found the json file. decode it
			var config types.ExportedMachineV1
			err = json.NewDecoder(tarReader).Decode(&config)
			if err != nil {
				return nil, fmt.Errorf("decode config: %w", err)
			}

			return &config, nil
		}
	}

	return nil, fmt.Errorf("can't find '_orbstack/config.v1.json' in archive. is this a valid OrbStack machine export?")
}

func (m *ConManager) ImportContainerFromHostPath(newName, hostPath string) (_ *Container, retErr error) {
	file, err := securefs.Open(mounts.Virtiofs, hostPath)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	// read config
	config, err := readConfigFromExport(file)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	// rewind file for bsdtar
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return nil, fmt.Errorf("seek file: %w", err)
	}

	// default to original name from export
	oldName := config.Record.Name
	if newName == "" {
		newName = oldName
	}

	newC, _, err := m.beginCreate(&types.CreateRequest{
		Name: newName,

		Image:  config.Record.Image,
		Config: config.Record.Config,
	})
	if err != nil {
		return nil, err
	}
	defer newC.holds.EndMutation()
	defer func() {
		if retErr != nil {
			err2 := newC.deleteInternal()
			if err2 != nil {
				logrus.WithError(err2).Error("failed to clean up failed container clone")
			}
		}
	}()

	err = newC.createDataDirs(createDataDirsOptions{
		includeRootfsDir: false,
	})
	if err != nil {
		return nil, fmt.Errorf("create data dirs: %w", err)
	}

	err = newC.jobManager.Run(func(ctx context.Context) error {
		// for compression, bsdtar has "--options zstd:threads=N", but there's no zstdmt for decompression
		cmd := exec.CommandContext(ctx, "bsdtar", "--zstd", "-C", newC.dataDir, "--xattrs", "--fflags", "-xf", "-")
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

		// delete the config dir
		err = os.RemoveAll(newC.dataDir + "/" + exportedConfigDir)
		if err != nil {
			return fmt.Errorf("remove config dir: %w", err)
		}

		// sanity check: do we have a rootfs/ dir?
		if err := unix.Access(newC.rootfsDir, unix.F_OK); err != nil {
			return fmt.Errorf("rootfs dir not found. is this a valid OrbStack machine export?")
		}

		// in theory, malicious imports can smuggle files into the machine's data dir, which will cause phantom data usage. but it'll still get deleted along with the machine, doesn't bypass quota, and will be included in subvolume qgroup usage.
		// it's probably better to just let the extra data sit there: if we add something other than rootfs/ in a future version of OrbStack, include it in exports, import it in an older version, and then upgrade, then ideally the extra metadata can still be used by the new version. so upgrading OrbStack restores full functionality for the machine, rather than having to re-import it.
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("make rootfs: %w", err)
	}

	// update hostname
	err = newC.updateHostnameLocked(oldName, newName)
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
