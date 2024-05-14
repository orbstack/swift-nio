package guihelper

import (
	"fmt"

	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/util/pspawn"
)

func run(args ...string) (string, error) {
	exe, err := conf.FindGuihelperExe()
	if err != nil {
		return "", err
	}

	cmd := pspawn.Command(exe, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run guihelper: %w; output: %s", err, out)
	}

	return string(out), nil
}

func Notify(n guitypes.Notification) error {
	soundArg := "--sound"
	if n.Silent {
		soundArg = "--no-sound"
	}

	_, err := run("notify", n.Title, n.Message, n.Subtitle, soundArg, n.URL)
	return err
}
