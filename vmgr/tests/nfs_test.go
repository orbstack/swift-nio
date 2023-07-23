package tests

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"syscall"
	"testing"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
)

const (
	//TODO
	singleTestMachine = "ubuntu"
)

func hostUsername() string {
	u, err := user.Current()
	if err != nil {
		panic(err)
	}
	return u.Username
}

func TestNfsReadOnlyRoot(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(coredir.NfsMountpoint()+"/testfile", []byte("test"), 0644)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
}

func TestNfsMachineRootPermissions(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(fmt.Sprintf("%s/%s/root/testfile", coredir.NfsMountpoint(), singleTestMachine), []byte("test"), 0644)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
}

func TestNfsMachineUserPermissions(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.NfsMountpoint(), singleTestMachine, hostUsername()), []byte("test"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// delete file
	err = os.Remove(fmt.Sprintf("%s/%s/home/%s/testfile", coredir.NfsMountpoint(), singleTestMachine, hostUsername()))
	if err != nil {
		t.Fatal(err)
	}
}
