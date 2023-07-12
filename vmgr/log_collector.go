package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/getsentry/sentry-go"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/sirupsen/logrus"
)

const (
	panicShutdownDelay = 100 * time.Millisecond
	panicLogLines      = 75
)

var (
	oomKillRegex = regexp.MustCompile(`Out of memory: Killed process \d+ \((.+)\)`)
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

func tryReadLogHistory(path string, numLines int) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	// read last lines
	scanner := bufio.NewScanner(file)
	// circular buffer
	lines := make([]string, 0, numLines)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > numLines {
			lines = lines[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	return strings.Join(lines, "\n"), nil
}

func NewConsoleLogPipe(stopCh chan<- StopRequest) (*os.File, error) {
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
		defer func() { _ = w.Close() }()
		// copy each line and prefix it
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text() + "\n"

			// don't add console prefix if first character is already emoji
			if len(line) > 0 && isMultibyteByte(line[0]) {
				_, _ = io.WriteString(os.Stdout, line)
			} else if len(line) > 0 && line[0] == '[' {
				// shut down on kernel panic
				// "Unable to handle kernel" is for null deref/segfault, in which case stack trace is printed before kernel panic
				// we only search for that prefix because on arm64 it's "Unable to handle kernel %s"
				if strings.Contains(line, "] Kernel panic - not syncing:") && panicBuffer == nil {
					// start recording panic lines
					panicBuffer = new(bytes.Buffer)

					time.AfterFunc(panicShutdownDelay, func() {
						stopCh <- StopRequest{Type: StopTypeForce, Reason: StopReasonPanic}

						// report panic lines to sentry
						// if possible we read the last lines of the log file
						var panicLog string
						if logHistory, err := tryReadLogHistory(conf.VmgrLog(), panicLogLines); err == nil {
							panicLog = logHistory
						} else {
							panicLog = panicBuffer.String()
						}

						sentry.CaptureException(&KernelPanicError{
							Err: errors.New("kernel panic:\n" + panicLog),
						})
					})
				} else if strings.Contains(line, "] Out of memory: Killed process") {
					// notify OOM kill
					// format:
					// [ 1041.081213] Out of memory: Killed process 225788 (stress-ng-shm) total-vm:195656kB, anon-rss:5100kB, file-rss:512kB, shmem-rss:0kB, UID:501 pgtables:136kB oom_score_adj:1000
					processName := oomKillRegex.FindStringSubmatch(line)[1]
					// this takes 100 ms, don't block the console reader
					go func() {
						err := guihelper.Notify(guitypes.Notification{
							Title:   "Out of memory: “" + processName + "”",
							Message: "Stopped task to save memory. Consider increasing memory limit in Settings.",
							URL:     "orbstack://settings",
						})
						if err != nil {
							logrus.WithError(err).Error("failed to send OOM kill notification")
						}
					}()
				}

				_, _ = io.WriteString(os.Stdout, kernelPrefix+magenta(line))

				if panicBuffer != nil {
					panicBuffer.WriteString(line)
				}
			} else {
				_, _ = io.WriteString(os.Stdout, consolePrefix+yellow(line))
			}
		}
	}()

	return w, nil
}
