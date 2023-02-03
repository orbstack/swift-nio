package buildid

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
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
	return strconv.FormatInt(stat.Ctim.Nano(), 10), nil
}
