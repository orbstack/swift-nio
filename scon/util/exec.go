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
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w; output: %s", err, string(output))
	}

	return nil
}

func RunWithInput(input string, combinedArgs ...string) error {
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w; output: %s", err, string(output))
	}

	return nil
}

func RunInheritOut(combinedArgs ...string) error {
	logrus.Debugf("run: %v", combinedArgs)
	cmd := exec.Command(combinedArgs[0], combinedArgs[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
