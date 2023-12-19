package tests

import (
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

// can't be parallel. messes up docker tests
func TestK8sControlCLI(t *testing.T) {
	// start k8s
	_, err := util.Run("orb", "start", "k8s")
	checkT(t, err)

	// restart
	_, err = util.Run("orb", "restart", "k8s")
	checkT(t, err)

	// stop k8s
	_, err = util.Run("orb", "stop", "k8s")
	checkT(t, err)

	// restart when stopped
	_, err = util.Run("orb", "restart", "k8s")
	checkT(t, err)

	// stop k8s
	_, err = util.Run("orb", "stop", "k8s")
	checkT(t, err)
}
