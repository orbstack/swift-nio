package agent

import (
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// also used for loginctl
	nixosPollInterval = 25 * time.Millisecond
	nixosBootTimeout  = 30 * time.Second
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
	// don't assume systemd though - just check /proc/1/comm != "init"

	start := time.Now()
	for {
		comm, err := os.ReadFile("/proc/1/comm")
		if err != nil {
			logrus.WithError(err).Error("failed to read /proc/1/comm")
			continue
		}

		if strings.TrimSpace(string(comm)) != "init" {
			break
		}

		time.Sleep(nixosPollInterval)
		if time.Since(start) > nixosBootTimeout {
			logrus.WithError(err).Error("timed out waiting for nixos stage2 boot")
			break
		}
	}
}
