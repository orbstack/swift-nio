package tests

import (
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestModprobeDockerBind(t *testing.T) {
	t.Parallel()

	_, err := util.Run("docker", "run", "--rm", "-v", "/lib/modules:/lib/modules", "alpine", "modprobe", "br_netfilter", "wireguard")
	if err != nil {
		t.Fatal(err)
	}
}
