package guihelper

import (
	"fmt"
	"os/exec"

	"github.com/kdrag0n/macvirt/macvmgr/conf"
)

func run(args ...string) (string, error) {
	exe, err := conf.FindGuihelperExe()
	if err != nil {
		return "", err
	}

	cmd := exec.Command(exe, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run guihelper: %w; output: %s", err, out)
	}

	return string(out), nil
}

type Notification struct {
	Title   string
	Message string

	Subtitle string
	Silent   bool
}

func Notify(n Notification) error {
	soundArg := "--sound"
	if n.Silent {
		soundArg = "--no-sound"
	}

	_, err := run("notify", n.Title, n.Message, n.Subtitle, soundArg)
	return err
}
