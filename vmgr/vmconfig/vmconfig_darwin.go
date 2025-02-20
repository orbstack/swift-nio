//go:build darwin

package vmconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/orbstack/macvirt/vmgr/swext"
	"github.com/orbstack/macvirt/vmgr/vmclient/vmtypes"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

const (
	gidAdmin = 80
)

func validateAPFS(dataDir string) error {
	testPath := dataDir + "/.orbstack-test"
	testPath2 := testPath + ".2"

	f, err := os.Create(testPath)
	if err != nil {
		return fmt.Errorf("create test file: %w", err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(testPath) }()

	err = unix.Clonefile(testPath, testPath2, 0)
	if err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			return errors.New("data storage location must be formatted as APFS")
		} else {
			return fmt.Errorf("check for APFS: %w", err)
		}
	}
	defer func() { _ = os.Remove(testPath2) }()

	return nil
}

var IsAdmin = sync.OnceValue(func() bool {
	// get current process supplementary groups to avoid querying server for network accounts
	gids, err := unix.Getgroups()
	if err != nil {
		return false
	}
	return slices.Contains(gids, gidAdmin)
})

func Defaults() (*vmtypes.VmConfig, error) {
	defaults := BaseDefaults()

	// merge with MDM config
	mdmJSON, err := swext.DefaultsGetMdmVmConfig()
	if err != nil {
		return nil, fmt.Errorf("get mdm config: %w", err)
	}

	// no deep merge needed, just unmarshal into it: we only have 1 level of keys
	if mdmJSON != "" {
		logrus.WithField("json", mdmJSON).Debug("overlaying MDM vmconfig")
		err = json.Unmarshal([]byte(mdmJSON), defaults)
		if err != nil {
			return nil, fmt.Errorf("unmarshal mdm config: %w", err)
		}
	}

	return defaults, nil
}
