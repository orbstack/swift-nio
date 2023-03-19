package agent

import (
	"os/exec"
	"time"

	"github.com/kdrag0n/macvirt/scon/util"
	"github.com/sirupsen/logrus"
)

func waitForLogind() {
	if _, err := exec.LookPath("loginctl"); err != nil {
		return
	}

	logrus.Info("waiting for systemd-logind")
	start := time.Now()
	for {
		err := util.Run("loginctl")
		if err == nil {
			break
		}

		time.Sleep(nixosPollInterval)
		if time.Since(start) > nixosBootTimeout {
			logrus.WithError(err).Error("timed out waiting for systemd-logind")
			break
		}
	}
}
