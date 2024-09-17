package tests

import (
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

func TestWormholeWaitpid1HangRegression(t *testing.T) {
	t.Parallel()

	// Docker CLI doesn't output warnings to stderr :/
	id, err := util.Run("docker", "run", "-d", "--name", "rr", "--rm", "--platform", "linux/amd64", "ghcr.io/r-hub/containers/gcc14:latest", "R", "-q", "-e", "TRUE")
	if err != nil {
		t.Fatal(err)
	}
	id = strings.TrimSpace(id)
	defer util.Run("docker", "stop", id)

	out, err := util.Run("orbctl", "debug", id, "echo", "meow 🏳️‍⚧️")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.TrimSpace(out)

	if out != "meow 🏳️‍⚧️" {
		t.Fatalf("didn't get expected output, got %v", out)
	}
}

func TestRemountWormholeNfsRwRegression(t *testing.T) {
	t.Parallel()

	// Docker CLI doesn't output warnings to stderr :/
	id, err := util.Run("docker", "run", "--read-only", "-d", "--name", "uwu", "--rm", "alpine:latest", "sleep", "1000000000000")
	if err != nil {
		t.Fatal(err)
	}
	id = strings.TrimSpace(id)
	defer util.Run("docker", "stop", id)

	time.Sleep(time.Second)

	_, err = os.Lstat(coredir.NfsMountpoint() + "/docker/containers/uwu/bin/ls")
	if err != nil {
		t.Fatal(err)
	}

	out, err := util.Run("orbctl", "debug", id, "echo", "nyaa~")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.TrimSpace(out)

	if out != "nyaa~" {
		t.Fatalf("didn't get expected output, got %v", out)
	}
}

func TestK8sContainersRegression(t *testing.T) {
	t.Parallel()

	// Docker CLI doesn't output warnings to stderr :/
	id, err := util.Run("docker", "run", "--cap-drop", "all", "-d", "--rm", "alpine:latest", "sleep", "1000000000000")
	if err != nil {
		t.Fatal(err)
	}
	id = strings.TrimSpace(id)
	defer util.Run("docker", "stop", id)

	out, err := util.Run("orbctl", "debug", id, "echo", "a cringe string to put into this test")
	if err != nil {
		t.Fatal(err)
	}
	out = strings.TrimSpace(out)

	if out != "a cringe string to put into this test" {
		t.Fatalf("didn't get expected output, got %v", out)
	}
}
