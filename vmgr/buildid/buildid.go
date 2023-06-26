package buildid

import (
	"math"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

const (
	mtimeGranularity = 5 // 5 sec
)

func CalculateCurrent() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return CalculatePath(exePath)
}

func CalculatePath(path string) (string, error) {
	var stat unix.Stat_t
	err := unix.Stat(path, &stat)
	if err != nil {
		return "", err
	}

	// ctime can't be changed. faster than hashing
	mtime := stat.Mtim.Nano()
	// convert to float sec
	mtimeSec := float64(mtime) / 1e9
	// apply granularity
	// weird changes when moving from /Volumes to /Applications:
	//    2023-02-14 14:08:01.136137984
	// -> 2023-02-14 14:08:01.136137962
	mtimeSec /= mtimeGranularity
	id := int64(math.Round(mtimeSec))
	return strconv.FormatInt(id, 10), nil
}
