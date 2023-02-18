package conf

import (
	"os"
	"path"
	"path/filepath"

	"github.com/kdrag0n/macvirt/scon/conf"
)

func findSiblingExe(name string) (string, error) {
	selfExe, err := os.Executable()
	if err != nil {
		return "", err
	}

	// resolve symlinks
	selfExe, err = filepath.EvalSymlinks(selfExe)
	if err != nil {
		return "", err
	}

	if conf.Debug() {
		return "/Users/dragon/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app/Contents/MacOS/" + name, nil
	}

	return path.Join(path.Dir(selfExe), name), nil
}

func FindSparkleExe() (string, error) {
	return findSiblingExe("sparkle-cli")
}

func FindGuihelperExe() (string, error) {
	return findSiblingExe("guihelper")
}
