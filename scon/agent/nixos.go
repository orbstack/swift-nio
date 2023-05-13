package agent

import (
	"os"

	"github.com/orbstack/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

// nixos filesystem is almost empty on first boot
// lxd template creates /etc/nixos/lxd.nix
// /etc/os-release symlink isn't created until >22.11
// https://github.com/NixOS/nixpkgs/commit/22a8cf0c28d536294d55049923575ce94bb39359
func isNixos() bool {
	_, err := os.Stat("/etc/nixos")
	return err == nil
}

func waitForNixBoot() {
	if !isNixos() {
		return
	}

	logrus.Info("waiting for nixos stage2 boot")

	// for setup and, we depend on su security wrappers in /run/wrappers and /etc/shells
	// that stuff is set up by stage2 init (/sbin/init) and activation script
	// wait for it to spawn systemd before we start the server
	err := util.WaitForRunPathExist("/run/systemd")
	if err != nil {
		logrus.WithError(err).Warn("failed to wait for nixos boot")
		return
	}
}
