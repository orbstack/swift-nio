package util

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/sirupsen/logrus"
)

func Run(combinedArgs ...string) error {
	logrus.Debugf("run: %v", combinedArgs)
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	// inherits env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return nil
}

func RunWithOutput(combinedArgs ...string) (string, error) {
	logrus.Debugf("run: %v", combinedArgs)
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	// inherits env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return string(output), nil
}

func RunWithInput(input string, combinedArgs ...string) error {
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.Stdin = strings.NewReader(input)
	// inherits env
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("run command '%v': %w; output: %s", combinedArgs, err, string(output))
	}

	return nil
}

func RunInheritOut(combinedArgs ...string) error {
	logrus.Debugf("run: %v", combinedArgs)
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// inherits env
	return cmd.Run()
}
