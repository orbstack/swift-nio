package buildid

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

func CalculateCurrent() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}
	return CalculatePath(exePath)
}

func CalculatePath(path string) (string, error) {
	// read it and hash it
	exe, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer exe.Close()

	hash := sha256.New()
	_, err = io.Copy(hash, exe)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}
