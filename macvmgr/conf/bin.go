package conf

import (
	"path"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
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
	return findSiblingExe("OrbStack Helper (UI)")
}

func FindAppBundle() (string, error) {
	if conf.Debug() {
		return "/Users/dragon/Library/Caches/JetBrains/AppCode2023.1/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app", nil
	}

	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}

	bundlePath := strings.TrimSuffix(exeDir, "/Contents/MacOS")
	return bundlePath, nil
}
