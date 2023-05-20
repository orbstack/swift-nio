package main

import (
	"bufio"
	"io"
	"os"

	"github.com/fatih/color"
)

func isMultibyteByte(firstByte byte) bool {
	return firstByte >= 0x80
}

func NewConsoleLogPipe() (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	kernelPrefix := color.New(color.FgMagenta, color.Bold).Sprint("👾 kernel | ")
	consolePrefix := color.New(color.FgYellow, color.Bold).Sprint("🐧 system | ")
	magenta := color.New(color.FgMagenta).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	go func() {
		defer w.Close()
		// copy each line and prefix it
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text() + "\n"

			// don't add console prefix if first character is already emoji
			if len(line) > 0 && isMultibyteByte(line[0]) {
				io.WriteString(os.Stdout, line)
			} else if len(line) > 0 && line[0] == '[' {
				io.WriteString(os.Stdout, kernelPrefix+magenta(line))
			} else {
				io.WriteString(os.Stdout, consolePrefix+yellow(line))
			}
		}
	}()

	return w, nil
}
