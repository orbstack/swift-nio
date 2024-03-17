package main

import (
	"os"

	"github.com/orbstack/macvirt/scon/securefs"
)

func isNixContainer(rootfsFile *os.File) (bool, error) {
	fs, err := securefs.NewFromDirfd(int(rootfsFile.Fd()))
	if err != nil {
		return false, err
	}
	// CAN'T CLOSE FS! it doesn't own the fd

	_, err = fs.Stat("/nix/store")
	return err == nil, nil
}
