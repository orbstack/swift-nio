package util

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/orbstack/macvirt/vmgr/util/pspawn"
	"github.com/sirupsen/logrus"
)

func Run(combinedArgs ...string) (string, error) {
	logrus.Tracef("run: %v", combinedArgs)
	cmd := pspawn.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// without this, running interactive shell breaks ctrl-c SIGINT
		Setsid: true,
	}
	// inherit env
	cmd.Env = os.Environ()
	// avoid triggering iterm2 shell integration
	cmd.Env = append(cmd.Env, "TERM=dumb")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return string(output), nil
}

func RunWithEnv(extraEnv []string, combinedArgs ...string) (string, error) {
	logrus.Tracef("run: %v", combinedArgs)
	cmd := pspawn.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// without this, running interactive shell breaks ctrl-c SIGINT
		Setsid: true,
	}
	// inherit env
	cmd.Env = os.Environ()
	// avoid triggering iterm2 shell integration
	cmd.Env = append(cmd.Env, "TERM=dumb")
	// add extra env
	cmd.Env = append(cmd.Env, extraEnv...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return string(output), nil
}

func RunLoginShell(ctx context.Context, combinedArgs ...string) error {
	logrus.Tracef("run: %v", combinedArgs)
	cmd := pspawn.CommandContext(ctx, combinedArgs[0], combinedArgs[1:]...)
	// transform to login shell syntax: -bash instead of bash -l
	cmd.Args[0] = "-" + filepath.Base(cmd.Args[0])
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// without this, running interactive shell breaks ctrl-c SIGINT
		Setsid: true,
	}
	// inherit env
	cmd.Env = os.Environ()
	// avoid triggering iterm2 shell integration
	cmd.Env = append(cmd.Env, "TERM=dumb")

	// context timeout doesn't work with .Output()
	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("run command '%v': %w", combinedArgs, err)
	}

	return nil
}
