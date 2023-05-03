package conf

import (
	"path"

	"github.com/kdrag0n/macvirt/scon/conf"
)

func findSiblingExe(name string) (string, error) {
	if conf.Debug() {
		return "/Users/dragon/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app/Contents/MacOS/" + name, nil
	}

	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}
	return path.Join(exeDir, name), nil
}

func FindSparkleExe() (string, error) {
	return findSiblingExe("sparkle-cli")
}

func FindGuihelperExe() (string, error) {
	return findSiblingExe("guihelper")
}
