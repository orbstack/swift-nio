package util

import (
	"fmt"
	"os/exec"
	"path/filepath"
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
	// avoid triggering iterm2 shell integration
	cmd.Env = append(cmd.Env, "TERM=dumb")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return string(output), nil
}

func RunLoginShell(combinedArgs ...string) (string, error) {
	logrus.Tracef("run: %v", combinedArgs)
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	// transform to login shell syntax: -bash instead of bash -l
	cmd.Args[0] = "-" + filepath.Base(cmd.Args[0])
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// without this, running interactive shell breaks ctrl-c SIGINT
		Setsid: true,
	}
	// avoid triggering iterm2 shell integration
	cmd.Env = append(cmd.Env, "TERM=dumb")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return string(output), nil
}
