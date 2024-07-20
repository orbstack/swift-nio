package debugutil

import (
	"os"
	"strconv"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/orbstack/macvirt/vmgr/vmm"
	"github.com/sirupsen/logrus"
)

func SampleStacks(machine vmm.Machine) {
	logrus.Warn("sampling stacks due to VM hang")

	// ask vCPUs to sample their stacks, but don't wait for them in case they're stuck
	if machine != nil {
		err := machine.DumpDebug()
		if err != nil {
			logrus.WithError(err).Error("failed to dump VM stacks")
		}
	}

	_, err := util.Run("sample", "-f", conf.VmgrSampleLog(), strconv.Itoa(os.Getpid()), "1" /*second*/)
	if err != nil {
		logrus.WithError(err).Error("failed to sample stacks")
	}

	logrus.Warn("stacks sampled")
}
