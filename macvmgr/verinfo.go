package main

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
)

func calcBuildID() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// read it and hash it
	exe, err := os.Open(exePath)
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
