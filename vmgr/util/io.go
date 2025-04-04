package util

import (
	"bytes"
	"errors"
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

func WriteFileAtomicIfChanged(path string, data []byte, mode fs.FileMode) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WriteFileAtomic(path, data, mode)
		} else {
			return err
		}
	}
	if bytes.Equal(existing, data) {
		return nil
	}
	return WriteFileAtomic(path, data, mode)
}

func WriteFileIfChanged(path string, data []byte, mode fs.FileMode) error {
	existing, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.WriteFile(path, data, mode)
		} else {
			return err
		}
	}
	if bytes.Equal(existing, data) {
		return nil
	}
	return os.WriteFile(path, data, mode)
}
