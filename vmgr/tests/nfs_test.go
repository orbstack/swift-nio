package tests

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/util"
)

const (
	//TODO
	singleTestMachine = "ubuntu"
)

func hostUsername(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	return u.Username
}

func TestNfsReadOnlyRoot(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(coredir.EnsureNfsMountpoint()+"/testfile", []byte("test"), 0644)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
}

func TestNfsMachineRootPermissions(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(fmt.Sprintf("%s/%s/root/testfile", coredir.EnsureNfsMountpoint(), singleTestMachine), []byte("test"), 0644)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
}

func TestNfsMachineUserPermissions(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.EnsureNfsMountpoint(), singleTestMachine, hostUsername(t)), []byte("test"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// delete file
	err = os.Remove(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.EnsureNfsMountpoint(), singleTestMachine, hostUsername(t)))
	if err != nil {
		t.Fatal(err)
	}
}

func TestNfsContainerWrite(t *testing.T) {
	t.Parallel()

	// start container
	_, err := util.Run("docker", "run", "--rm", "-d", "--name", "otest-nfs", "alpine", "sleep", "infinity")
	if err != nil {
		t.Fatal(err)
	}
	defer util.Run("docker", "rm", "-f", "otest-nfs")

	// wait for start
	time.Sleep(3 * time.Second)

	// write file via nfs
	err = os.WriteFile(coredir.EnsureNfsMountpoint()+"/docker/containers/otest-nfs/etc/testfile", []byte("test"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// read file via container
	out, err := util.Run("docker", "exec", "otest-nfs", "cat", "/etc/testfile")
	if err != nil {
		t.Fatal(err)
	}

	if out != "test" {
		t.Fatalf("expected test, got: %s", out)
	}

	// delete file
	err = os.Remove(coredir.EnsureNfsMountpoint() + "/docker/containers/otest-nfs/etc/testfile")
	if err != nil {
		t.Fatal(err)
	}

	// read file via container
	out, err = util.Run("docker", "exec", "otest-nfs", "cat", "/etc/testfile")
	if err == nil || !strings.Contains(err.Error(), "No such file or directory") {
		t.Fatal(err)
	}

	// read file via nfs
	data, err := os.ReadFile(coredir.EnsureNfsMountpoint() + "/docker/containers/otest-nfs/etc/passwd")
	if err != nil {
		t.Fatal(err)
	}

	// check data
	if !strings.Contains(string(data), "root") {
		t.Fatalf("expected root, got: %s", string(data))
	}
}
