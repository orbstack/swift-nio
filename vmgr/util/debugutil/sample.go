package debugutil

import (
	"os"
	"strconv"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/util"
	"github.com/sirupsen/logrus"
)

func SampleStacks() {
	logrus.Warn("sampling stacks due to VM hang")
	_, err := util.Run("sample", "-f", conf.VmgrSampleLog(), strconv.Itoa(os.Getpid()), "1" /*second*/)
	if err != nil {
		logrus.WithError(err).Error("failed to sample stacks")
	}

	logrus.Warn("stacks sampled")
}
