package tests

import (
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestKindCreateCluster(t *testing.T) {
	t.Parallel()

	// create cluster
	_, err := util.Run("kind", "create", "cluster", "--name", "otest")
	checkT(t, err)

	// delete cluster
	_, err = util.Run("kind", "delete", "cluster", "--name", "otest")
	checkT(t, err)
}
