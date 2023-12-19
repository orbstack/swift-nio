package tests

import (
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestScliCreateStopStartDelete(t *testing.T) {
	t.Parallel()

	defer util.Run("orb", "delete", "otest2")

	// create
	_, err := util.Run("orb", "create", "alpine", "otest2")
	checkT(t, err)

	// stop
	_, err = util.Run("orb", "stop", "otest2")
	checkT(t, err)

	// start
	_, err = util.Run("orb", "start", "otest2")
	checkT(t, err)

	// restart
	_, err = util.Run("orb", "restart", "otest2")
	checkT(t, err)

	// run command
	out, err := util.Run("orb", "-m", "otest2", "true")
	checkT(t, err)
	// make sure output is empty, no spinner
	if out != "" {
		t.Fatalf("expected empty output, got: %s", out)
	}

	// delete
	_, err = util.Run("orb", "delete", "otest2")
	checkT(t, err)
}
