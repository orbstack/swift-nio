package util

import (
	"io/fs"
	"os"
)

func WriteFileAtomic(path string, data []byte, mode fs.FileMode) error {
	err := os.WriteFile(path+".tmp", data, mode)
	if err != nil {
		return err
	}
	defer os.Remove(path + ".tmp")

	return os.Rename(path+".tmp", path)
}
