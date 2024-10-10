package tests

import (
	"os"
	"testing"

	"github.com/orbstack/macvirt/vmgr/util"
)

func TestMutagenProject(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	_, err = util.Run("mutagen", "project", "start", "-f", cwd+"/mutagen/web-go/mutagen.yml")
	if err != nil {
		t.Fatal(err)
	}

	_, err = util.Run("mutagen", "project", "terminate", "-f", cwd+"/mutagen/web-go/mutagen.yml")
	if err != nil {
		t.Fatal(err)
	}
}
