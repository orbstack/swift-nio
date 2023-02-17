package conf

import (
	"os"
	"path"
	"path/filepath"

	"github.com/kdrag0n/macvirt/scon/conf"
)

func FindSparkleExe() (string, error) {
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
		return "/Users/dragon/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/SourcePackages/artifacts/sparkle/sparkle.app/Contents/MacOS/sparkle", nil
	}

	return path.Join(path.Dir(selfExe), "sparkle-cli"), nil
}
