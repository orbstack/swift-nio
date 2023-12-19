package tests

import (
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestBuildxContainerDriver(t *testing.T) {
	t.Parallel()

	defer util.Run("docker", "buildx", "rm", "otest")

	// create
	_, err := util.Run("docker", "buildx", "create", "--name", "otest")
	checkT(t, err)

	// build
	_, err = util.Run("docker", "buildx", "build", "--builder", "otest", "--platform", "linux/arm/v7,linux/arm64/v8,linux/amd64", "-t", "otest", "--load", ".")

	// delete
	_, err = util.Run("docker", "buildx", "rm", "otest")
	checkT(t, err)
}
