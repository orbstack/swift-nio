package tests

import (
	"strings"
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestRosettaSwiftDriver(t *testing.T) {
	t.Parallel()

	out, err := util.Run("docker", "run", "--platform", "linux/x86_64", "swift:5.8-amazonlinux2", "bash", "-cl", "swift", "build", "-c", "release", "--show-bin-path")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "Welcome to Swift") {
		t.Fatalf("swift output does not contain 'Welcome to Swift': %s", out)
	}
}
