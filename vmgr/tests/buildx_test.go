package tests

import (
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

// TODO(winter): I've seen this test sporadically fail at times, but haven't repro'd consistently. Figure out why.
func TestBuildxContainerDriver(t *testing.T) {
	t.Parallel()

	// create
	n := name("buildx-cd")
	_, err := util.Run("docker", "buildx", "create", "--name", n)
	checkT(t, err)

	// build
	_, err = util.Run("docker", "buildx", "build", "--builder", n, "--platform", "linux/arm/v7,linux/arm64/v8,linux/amd64", "-t", "otest", "--load", ".")

	// delete
	_, err = util.Run("docker", "buildx", "rm", n)
	checkT(t, err)
}
