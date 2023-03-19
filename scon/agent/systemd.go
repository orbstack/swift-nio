package agent

import (
	"os/exec"
	"time"

	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

func waitForSystemdBoot() {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return
	}

	logrus.Info("waiting for systemd")
	start := time.Now()
	for {
		err := util.Run("systemctl", "is-system-running", "--wait")
		if err == nil {
			break
		}

		time.Sleep(nixosPollInterval)
		if time.Since(start) > nixosBootTimeout {
			logrus.WithError(err).Error("timed out waiting for systemd")
			break
		}
	}
}
