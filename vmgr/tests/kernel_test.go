package tests

import (
	"strings"
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestKernelVersionDirty(t *testing.T) {
	t.Parallel()

	out, err := util.Run("docker", "run", "--rm", "alpine", "uname", "-r")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(out, "-dirty") {
		t.Fatalf("kernel version contains -dirty: %s", out)
	}
}

func TestModprobeDockerBind(t *testing.T) {
	t.Parallel()

	_, err := util.Run("docker", "run", "--rm", "-v", "/lib/modules:/lib/modules", "alpine", "modprobe", "br_netfilter", "wireguard")
	if err != nil {
		t.Fatal(err)
	}
}
