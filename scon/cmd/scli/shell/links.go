package shell

import (
	"fmt"
	"os"
	"strings"

	"github.com/kdrag0n/macvirt/macvmgr/conf/mounts"
)

func LinkCmd(name string) error {
	// can't contain slashes
	if strings.Contains(name, "/") {
		return fmt.Errorf("invalid command name: %s", name)
	}

	// fail if default link
	if _, err := os.Lstat(mounts.DefaultCmdLinks + "/" + name); err == nil {
		return fmt.Errorf("already linked by default: %s", name)
	}
	if _, err := os.Lstat(mounts.DefaultHiprioCmdLinks + "/" + name); err == nil {
		return fmt.Errorf("already linked by default: %s", name)
	}

	// create link
	err := os.Symlink(mounts.Macctl, mounts.UserCmdLinks+"/"+name)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("already linked: %s", name)
		} else {
			return err
		}
	}

	return nil
}

func UnlinkCmd(name string) error {
	// can't contain slashes
	if strings.Contains(name, "/") {
		return fmt.Errorf("invalid command name: %s", name)
	}

	// fail if default link
	if _, err := os.Lstat(mounts.DefaultCmdLinks + "/" + name); err == nil {
		return fmt.Errorf("can't remove default link: %s", name)
	}
	if _, err := os.Lstat(mounts.DefaultHiprioCmdLinks + "/" + name); err == nil {
		return fmt.Errorf("can't remove default link: %s", name)
	}

	// fail if not symlink
	fi, err := os.Lstat(mounts.UserCmdLinks + "/" + name)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not linked: %s", name)
		} else {
			return err
		}
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("not a symlink: %s", name)
	}

	// remove link
	err = os.Remove(mounts.UserCmdLinks + "/" + name)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("not linked: %s", name)
		} else {
			return err
		}
	}

	return nil
}
