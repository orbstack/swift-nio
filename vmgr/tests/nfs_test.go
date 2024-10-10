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

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/util"
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

func TestNfsMachinePermissions(t *testing.T) {
	t.Parallel()

	name := testPrefix() + "-nfs"
	container, err := scli.Client().Create(types.CreateRequest{
		Name: name,
		Image: types.ImageSpec{
			Distro: images.DistroAlpine,
		},
		InternalForTesting: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := scli.Client().ContainerDelete(container)
		if err != nil {
			fmt.Printf("failed to cleanup nfs container: %v", err)
		}
	})

	t.Run("root", func(t *testing.T) {
		err := os.WriteFile(fmt.Sprintf("%s/%s/root/testfile", coredir.EnsureNfsMountpoint(), name), []byte("test"), 0644)
		if !errors.Is(err, syscall.EACCES) {
			t.Fatal(err)
		}
	})

	t.Run("user", func(t *testing.T) {
		err := os.WriteFile(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.EnsureNfsMountpoint(), name, hostUsername(t)), []byte("test"), 0644)
		if err != nil {
			t.Fatal(err)
		}

		// delete file
		err = os.Remove(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.EnsureNfsMountpoint(), name, hostUsername(t)))
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestNfsContainerWrite(t *testing.T) {
	t.Parallel()

	// start container
	_, err := util.Run("docker", "run", "--rm", "-d", "--name", "otest-nfs", "alpine", "sleep", "infinity")
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, func() error {
		_, err := util.Run("docker", "rm", "-f", "otest-nfs")
		return err
	})

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
