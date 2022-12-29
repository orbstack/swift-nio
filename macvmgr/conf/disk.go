package conf

import "golang.org/x/sys/unix"

const (
	// max 8 TiB (size of sparse file), in MiB
	maxDiskSize = 8 * 1024 * 1024
)

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func DiskSize() uint64 {
	var statfs unix.Statfs_t
	err := unix.Statfs(DataDir(), &statfs)
	if err != nil {
		panic(err)
	}

	// blocks * block size
	totalBytes := statfs.Blocks * uint64(statfs.Bsize)
	return min64(totalBytes/1024/1024, maxDiskSize)
}
