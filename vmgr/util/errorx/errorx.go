package errorx

import (
	"errors"
	"fmt"
	"github.com/fatih/color"
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

func RecoverCLI(exitCode int) {
	if r := recover(); r != nil {
		if r == errCLIPanic {
			// exit after panic propagation
			os.Exit(exitCode)
		} else {
			panic(r)
		}
	}
}

func PrintRecursive(err error) {
	if err == nil {
		return
	}

	fmt.Println(err)
	PrintRecursive(errors.Unwrap(err))
}

func CheckCLI(err error) {
	if err != nil {
		red := color.New(color.FgRed).FprintlnFunc()
		red(os.Stderr, err)

		// may need to do cleanup, so don't exit
		panic(errCLIPanic)
	}
}

func ErrorfCLI(format string, args ...interface{}) {
	red := color.New(color.FgRed).FprintfFunc()
	red(os.Stderr, format, args...)
}
