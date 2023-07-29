package errorx

import (
	"errors"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
)

var (
	errCLIPanic = errors.New("cli panic")
)

func Fatalf(format string, args ...interface{}) {
	err := fmt.Errorf(format, args...)
	logrus.StandardLogger().Log(logrus.FatalLevel, err)

	// do a proper panic to hit recover path (clean up locks, etc)
	panic(errCLIPanic)
}

func RecoverCLI() {
	if r := recover(); r != nil {
		if r == errCLIPanic {
			// exit after panic propagation
			os.Exit(1)
		} else {
			panic(r)
		}
	}
}
