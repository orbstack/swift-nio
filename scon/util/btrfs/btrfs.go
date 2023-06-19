package btrfs

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// total 4K
const btrfsPathNameMax = 4087

type btrfsIoctlVolArgs struct {
	fd   int64
	name [btrfsPathNameMax + 1]byte
}

const cBTRFS_IOC_RESIZE = 1342215171

func FilesystemResize(dirPath string, size string) error {
	fd, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open fs dir: %w", err)
	}
	defer unix.Close(fd)

	args := btrfsIoctlVolArgs{
		// unused for this ioctl
		fd: 0,
	}
	copy(args.name[:], size)

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), cBTRFS_IOC_RESIZE, uintptr(unsafe.Pointer(&args)))
	if errno != 0 {
		return fmt.Errorf("ioctl(BTRFS_IOC_RESIZE): %w", errno)
	}

	return nil
}
