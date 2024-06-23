package conf

import (
	"strings"
)

func findAuxiliaryExe(name string) (string, error) {
	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}
	return exeDir + "/", nil
}

func FindSparkleExe() (string, error) {
	return findAuxiliaryExe("sparkle-cli")
}

func FindGuihelperExe() (string, error) {
	return findAuxiliaryExe("OrbStack Helper (UI)")
}

func FindAppBundle() (string, error) {
	exeDir, err := ExecutableDir()
	if err != nil {
		return "", err
	}

	bundlePath := strings.TrimSuffix(exeDir, "/Contents/MacOS")
	return bundlePath, nil
}
