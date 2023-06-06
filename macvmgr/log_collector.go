package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/getsentry/sentry-go"
)

const (
	panicShutdownDelay = 100 * time.Millisecond
)

type KernelPanicError struct {
	Err error
}

func (e *KernelPanicError) Error() string {
	return e.Err.Error()
}

func isMultibyteByte(firstByte byte) bool {
	return firstByte >= 0x80
}

func NewConsoleLogPipe(stopCh chan<- StopType) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	kernelPrefix := color.New(color.FgMagenta, color.Bold).Sprint("👾 kernel | ")
	consolePrefix := color.New(color.FgYellow, color.Bold).Sprint("🐧 system | ")
	magenta := color.New(color.FgMagenta).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	var panicBuffer *bytes.Buffer

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
				// shut down on kernel panic
				if strings.Contains(line, "] Kernel panic - not syncing:") {
					// start recording panic lines
					panicBuffer = new(bytes.Buffer)

					time.AfterFunc(panicShutdownDelay, func() {
						stopCh <- StopForce

						// report panic lines to sentry
						sentry.CaptureException(&KernelPanicError{
							Err: errors.New("kernel panic:\n" + panicBuffer.String()),
						})
					})
				}

				io.WriteString(os.Stdout, kernelPrefix+magenta(line))

				if panicBuffer != nil {
					panicBuffer.WriteString(line)
				}
			} else {
				io.WriteString(os.Stdout, consolePrefix+yellow(line))
			}
		}
	}()

	return w, nil
}
