//go:build darwin

package vmconfig

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
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
