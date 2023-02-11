package util

import (
	"fmt"
	"os/exec"
	"syscall"

	"github.com/sirupsen/logrus"
)

func Run(combinedArgs ...string) (string, error) {
	logrus.Tracef("run: %v", combinedArgs)
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// without this, running interactive shell breaks ctrl-c SIGINT
		Setsid: true,
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w; output: %s", err, string(output))
	}

	return string(output), nil
}
