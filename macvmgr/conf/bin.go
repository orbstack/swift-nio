package conf

import (
	"path"
	"strings"

	"github.com/orbstack/macvirt/scon/conf"
)

const (
	debugXcodeBundle   = "/Library/Developer/Xcode/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app"
	debugAppCodeBundle = "/Library/Caches/JetBrains/AppCode2023.1/DerivedData/MacVirt-cvlazugpvgfgozfesiozsrqnzfat/Build/Products/Debug/OrbStack.app"
)

func findAuxiliaryExe(name string) (string, error) {
	if conf.Debug() {
		return HomeDir() + debugAppCodeBundle + "/Contents/MacOS/" + name, nil
	}

	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}
	return path.Join(exeDir, name), nil
}

func FindSparkleExe() (string, error) {
	return findAuxiliaryExe("sparkle-cli")
}

func FindGuihelperExe() (string, error) {
	return findAuxiliaryExe("OrbStack Helper (UI)")
}

func FindAppBundle() (string, error) {
	if conf.Debug() {
		return HomeDir() + debugAppCodeBundle, nil
	}

	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}

	bundlePath := strings.TrimSuffix(exeDir, "/Contents/MacOS")
	return bundlePath, nil
}
