package cmd

import (
	"fmt"
	"os"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func checkCLI(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
