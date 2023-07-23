package tests

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/orbstack/macvirt/vmgr/conf/coredir"
)

func TestNfsReadOnlyRoot(t *testing.T) {
	t.Parallel()

	err := os.WriteFile(coredir.NfsMountpoint()+"/test", []byte("test"), 0644)
	if !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
}
