package cmd

import (
	"os"

	"github.com/fatih/color"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func checkCLI(err error) {
	if err != nil {
		red := color.New(color.FgRed).FprintlnFunc()
		red(os.Stderr, err)
		os.Exit(1)
	}
}
