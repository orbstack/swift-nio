package appver

import (
	"errors"
	"fmt"
	"runtime/debug"
)

var (
	cachedGitCommit string
)

func GitCommit() (string, error) {
	if cachedGitCommit != "" {
		return cachedGitCommit, nil
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("no build info available")
	}

	fmt.Println("build info:", info)
	for _, setting := range info.Settings {
		fmt.Println("setting:", setting)
		if setting.Key == "vcs.revision" {
			cachedGitCommit = setting.Value
			return cachedGitCommit, nil
		}
	}

	return "", errors.New("no git commit found")
}
