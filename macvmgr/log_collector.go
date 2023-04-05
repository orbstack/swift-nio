package main

import (
	"bufio"
	"io"
	"os"

	"github.com/fatih/color"
)

func NewConsoleLogPipe() (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	prefix := color.New(color.FgMagenta, color.Bold).Sprint("console | ")
	magenta := color.New(color.FgMagenta).SprintFunc()

	go func() {
		defer w.Close()
		// copy each line and prefix it
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			io.WriteString(os.Stdout, prefix+magenta(scanner.Text())+"\n")
		}
	}()

	return w, nil
}
