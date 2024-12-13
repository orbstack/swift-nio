//go:build darwin

package conf

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/swext"
	"golang.org/x/sys/unix"
)

func GroupContainerDir() string {
	// created by macOS APIs
	dir, err := swext.FilesGetContainerDir()
	if err != nil {
		panic(err)
	}
	if err := unix.Access(dir, unix.W_OK); err != nil {
		panic(fmt.Errorf("missing TCC permission for data storage directory: %w", err))
	}
	return dir
}
