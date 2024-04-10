//go:build darwin

package util

import (
	"fmt"

	"golang.org/x/sys/unix"
)

const xattrBackupExcludeKey = "com.apple.metadata:com_apple_backup_excludeItem"

// from ~/Pictures/Photos Library.photoslibrary/database
// same for most files: https://eclecticlight.co/2017/12/13/xattr-com-apple-metadatacom_apple_backup_excludeitem-exclude-from-backups/
// "tmutil isexcluded" to verify
var xattrBackupExcludeValue = []byte("bplist00_\x10\x11com.apple.backupd\x08\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x1C")

func SetBackupExclude(path string, exclude bool) error {
	if exclude {
		err := unix.Setxattr(path, xattrBackupExcludeKey, xattrBackupExcludeValue, 0)
		if err != nil {
			return fmt.Errorf("set xattr: %w", err)
		}
	} else {
		err := unix.Removexattr(path, xattrBackupExcludeKey)
		if err != nil && err != unix.ENOATTR {
			return fmt.Errorf("remove xattr: %w", err)
		}
	}

	return nil
}
