package syssetup

import (
	"errors"
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
)

func IsSshConfigWritable() bool {
	file, err := os.OpenFile(conf.UserSshDir()+"/config", os.O_WRONLY, 0)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return false
		} else if errors.Is(err, os.ErrNotExist) {
			// ~/.ssh is guaranteed to exist now, so check if it's writable
			info, err := os.Stat(conf.UserSshDir())
			if err != nil {
				return false
			}

			return info.Mode().Perm()&0222 != 0
		} else {
			return false
		}
	}
	file.Close()
	return true
}

func MakeHomeRelative(path string) string {
	return strings.Replace(path, conf.HomeDir(), "~", 1)
}
