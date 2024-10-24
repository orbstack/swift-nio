package tests

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/util"
)

func TestUPXRegression(t *testing.T) {
	t.Parallel()

	_, err := util.Run("docker", "run", "--rm", "bitnami/zookeeper:3.4.14", "nami", "--help")
	if err != nil {
		t.Fatal(err)
	}
}

// needed as wormhole started logging when it didn't before :(
func findLine(s string, want string) bool {
	ok := false
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)

		if line == want {
			ok = true
			break
		}
	}

	return ok
}

func TestWormholeWaitpid1HangRegression(t *testing.T) {
	t.Parallel()

	n := name("regr-pid1")
	_, err := util.Run("docker", "run", "-d", "--name", n, "--rm", "--platform", "linux/amd64", "ghcr.io/r-hub/containers/gcc14:latest", "R", "-q", "-e", "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, func() error {
		_, err := util.Run("docker", "stop", n)
		return err
	})

	out, err := runScli("orbctl", "debug", n, "echo", "meow 🏳️‍⚧️")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.TrimSpace(out)

	if !findLine(out, "meow 🏳️‍⚧️") {
		t.Fatal("didn't get expected output")
	}
}

func TestRemountWormholeNfsRwRegression(t *testing.T) {
	t.Parallel()

	n := name("regr-nfs-rw")
	_, err := util.Run("docker", "run", "--read-only", "-d", "--name", n, "--rm", "alpine:latest", "sleep", "1000000000000")
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, func() error {
		_, err := util.Run("docker", "stop", n)
		return err
	})

	time.Sleep(time.Second)

	_, err = os.Lstat(fmt.Sprintf("%s/docker/containers/%s/bin/ls", coredir.NfsMountpoint(), n))
	if err != nil {
		t.Fatal(err)
	}

	out, err := runScli("orbctl", "debug", n, "echo", "nyaa~")
	if err != nil {
		t.Fatal(err)
	}

	if !findLine(out, "nyaa~") {
		t.Fatal("didn't get expected output")
	}
}

func TestK8sContainersRegression(t *testing.T) {
	t.Parallel()

	n := name("regr-k8s-containers")
	_, err := util.Run("docker", "run", "--cap-drop", "all", "-d", "--name", n, "--rm", "alpine:latest", "sleep", "1000000000000")
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, func() error {
		_, err := util.Run("docker", "stop", n)
		return err
	})

	out, err := runScli("orbctl", "debug", n, "echo", "a cringe string to put into this test")
	if err != nil {
		t.Fatal(err)
	}

	if !findLine(out, "a cringe string to put into this test") {
		t.Fatal("didn't get expected output")
	}
}
