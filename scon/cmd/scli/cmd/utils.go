package cmd

import (
	"errors"
	"os"

	"github.com/fatih/color"
)

var (
	errCLIPanic = errors.New("cli panic")
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func printErrCLI(err error) {
	if err != nil {
		red := color.New(color.FgRed).FprintlnFunc()
		red(os.Stderr, err)
	}
}

func checkCLI(err error) {
	if err != nil {
		red := color.New(color.FgRed).FprintlnFunc()
		red(os.Stderr, err)

		// may need to do cleanup, so don't exit
		panic(errCLIPanic)
	}
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
