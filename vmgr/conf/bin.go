package conf

import (
	"strings"
)

func findAuxiliaryExe(name string) (string, error) {
	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}
	return exeDir + "/" + name, nil
}

func FindGUIExe() (string, error) {
	return findAuxiliaryExe("OrbStack")
}

func FindSparkleExe() (string, error) {
	return findAuxiliaryExe("sparkle-cli")
}

func FindGuihelperExe() (string, error) {
	return findAuxiliaryExe("OrbStack Helper (UI)")
}

func FindPstrampExe() (string, error) {
	return findAuxiliaryExe("pstramp")
}

func FindAppBundle() (string, error) {
	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}

	bundlePath := strings.TrimSuffix(exeDir, "/Contents/MacOS")
	return bundlePath, nil
}
