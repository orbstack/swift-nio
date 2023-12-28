package dmigrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	credclient "github.com/docker/docker-credential-helpers/client"
	"github.com/orbstack/macvirt/vmgr/conf"
)

func (m *Migrator) migrateCredentials() error {
	desktop := credclient.NewShellProgramFunc("docker-credential-desktop")
	osxkeychain := credclient.NewShellProgramFunc(conf.FindXbin("docker-credential-osxkeychain"))

	// only migrate if docker-credential-desktop is in PATH
	desktopPath, err := exec.LookPath("docker-credential-desktop")
	if err != nil {
		return nil
	}
	// find orig Docker.app path
	desktopPath, err = filepath.EvalSymlinks(desktopPath)
	if err != nil {
		return err
	}

	// add fake PATH to make docker-credential-desktop call ddesktop's docker-credential-osxkeychain
	tmpDir, err := os.MkdirTemp("", "orbstack-migrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	err = os.Symlink(strings.TrimSuffix(desktopPath, "-desktop")+"-osxkeychain", tmpDir+"/docker-credential-osxkeychain")
	if err != nil {
		return err
	}
	err = os.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))
	if err != nil {
		return err
	}

	// get all creds
	creds, err := credclient.List(desktop)
	if err != nil {
		return err
	}

	for registry /*username*/ := range creds {
		// get full cred
		cred, err := credclient.Get(desktop, registry)
		if err != nil {
			return err
		}

		// store in osxkeychain
		// this fixes keychain access permissions
		// no need to delete old one - this overwrites it
		if err := credclient.Store(osxkeychain, cred); err != nil {
			return err
		}
	}

	return nil
}
