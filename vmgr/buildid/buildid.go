package buildid

import (
	"os"
	"strconv"
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
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}

	// ctime can't be changed. faster than hashing
	mtimeSec := st.ModTime().Unix()
	// apply granularity
	// weird changes when moving from /Volumes to /Applications:
	//    2023-02-14 14:08:01.136137984
	// -> 2023-02-14 14:08:01.136137962
	id := mtimeSec / mtimeGranularity * mtimeGranularity
	return strconv.FormatInt(id, 10), nil
}
