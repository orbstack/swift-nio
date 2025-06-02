package tests

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"testing"
	"time"

	"github.com/orbstack/macvirt/scon/cmd/scli/scli"
	"github.com/orbstack/macvirt/scon/images"
	"github.com/orbstack/macvirt/scon/types"
	"github.com/orbstack/macvirt/vmgr/conf/coredir"
	"github.com/orbstack/macvirt/vmgr/util"
	"golang.org/x/sys/unix"
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
	if !errors.Is(err, unix.EACCES) {
		t.Fatal(err)
	}
}

func TestNfsMachinePermissions(t *testing.T) {
	t.Parallel()

	n := name("nfs-perms")
	container, err := scli.Client().Create(types.CreateRequest{
		Name: n,
		Image: types.ImageSpec{
			Distro: images.DistroAlpine,
		},
		InternalUseTestCache: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		err := scli.Client().ContainerDelete(container.ID)
		if err != nil {
			fmt.Printf("failed to cleanup nfs container: %v", err)
		}
	})

	t.Run("root", func(t *testing.T) {
		err := os.WriteFile(fmt.Sprintf("%s/%s/root/testfile", coredir.EnsureNfsMountpoint(), n), []byte("test"), 0644)
		if !errors.Is(err, unix.EACCES) {
			t.Fatal(err)
		}
	})

	t.Run("user", func(t *testing.T) {
		err := os.WriteFile(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.EnsureNfsMountpoint(), n, hostUsername(t)), []byte("test"), 0644)
		if err != nil {
			t.Fatal(err)
		}

		// delete file
		err = os.Remove(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.EnsureNfsMountpoint(), n, hostUsername(t)))
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestNfsContainerWrite(t *testing.T) {
	t.Parallel()

	// start container
	n := name("nfs-w")
	_, err := util.Run("docker", "run", "--rm", "-d", "--name", n, "alpine", "sleep", "infinity")
	if err != nil {
		t.Fatal(err)
	}
	cleanup(t, func() error {
		_, err := util.Run("docker", "rm", "-f", n)
		return err
	})

	// wait for start
	time.Sleep(3 * time.Second)

	// write file via nfs
	testFilePath := fmt.Sprintf("%s/docker/containers/%s/etc/testfile", coredir.EnsureNfsMountpoint(), n)
	err = os.WriteFile(testFilePath, []byte("test"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// read file via container
	out, err := util.Run("docker", "exec", n, "cat", "/etc/testfile")
	if err != nil {
		t.Fatal(err)
	}

	if out != "test" {
		t.Fatalf("expected test, got: %s", out)
	}

	// delete file
	err = os.Remove(testFilePath)
	if err != nil {
		t.Fatal(err)
	}

	// read file via container
	out, err = util.Run("docker", "exec", n, "cat", "/etc/testfile")
	if err == nil || !strings.Contains(err.Error(), "No such file or directory") {
		t.Fatal(err)
	}

	// read file via nfs
	data, err := os.ReadFile(fmt.Sprintf("%s/docker/containers/%s/etc/passwd", coredir.EnsureNfsMountpoint(), n))
	if err != nil {
		t.Fatal(err)
	}

	// check data
	if !strings.Contains(string(data), "root") {
		t.Fatalf("expected root, got: %s", string(data))
	}
}
