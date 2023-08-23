package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"os"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/getsentry/sentry-go"
	"github.com/orbstack/macvirt/vmgr/conf"
	"github.com/orbstack/macvirt/vmgr/conf/appid"
	"github.com/orbstack/macvirt/vmgr/guihelper"
	"github.com/orbstack/macvirt/vmgr/guihelper/guitypes"
	"github.com/orbstack/macvirt/vmgr/types"
	"github.com/sirupsen/logrus"
)

const (
	panicShutdownDelay = 250 * time.Millisecond
	panicLogLines      = 75
)

const kernelWarnRecordDuration = 500 * time.Millisecond

var (
	// this is just informational. we rely on health check (vcontrol client) to detect real VM freeze
	// in case of false-positive during sleep on x86
	kernelWarnPatterns = []string{
		// WARN (incl. btrfs i/o error, e.g. on disk img disappear)
		"] ------------[ cut here ]------------",
		// no need for BUG - we panic on oops
		// "] BUG: ",
		// netdev watchdog: ] NETDEV WATCHDOG: eth0 (virtio_net): transmit queue 0 timed out
		"] NETDEV WATCHDOG:",
		// RCU stall
		"] rcu: INFO:",
		// kfence memory safety issue
		"] BUG: KFENCE",
		// TODO: add NFS broken conn?
	}
)

var (
	oomKillRegex = regexp.MustCompile(`Out of memory: Killed process \d+ \((.+)\)`)
)

func init() {
	// remove RCU stall on x86. it's normal due to timekeeping
	if runtime.GOARCH == "amd64" {
		kernelWarnPatterns = slices.DeleteFunc(kernelWarnPatterns, func(s string) bool {
			return s == "] rcu: INFO:"
		})
	}
}

type KernelPanicError struct {
	Err error
}

func (e *KernelPanicError) Error() string {
	return "kernel panic: " + e.Err.Error()
}

type DataCorruptionError struct {
	Err error
}

func (e *DataCorruptionError) Error() string {
	return "data corruption: " + e.Err.Error()
}

type KernelWarning struct {
	Err error
}

func (e *KernelWarning) Error() string {
	return "kernel warning: " + e.Err.Error()
}

type KernelLogRecorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	active bool
}

func (r *KernelLogRecorder) Write(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.active {
		return len(p), nil
	}
	return r.buf.Write(p)
}

func (r *KernelLogRecorder) WriteString(s string) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.active {
		return len(s), nil
	}
	return r.buf.WriteString(s)
}

func (r *KernelLogRecorder) Start(duration time.Duration, callback func(output string)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.active {
		return
	}

	r.active = true
	r.buf.Reset()

	time.AfterFunc(duration, func() {
		r.mu.Lock()
		defer r.mu.Unlock()

		if !r.active {
			return
		}

		r.active = false
		callback(r.buf.String())
	})
}

func matchWarnPattern(line string) bool {
	for _, pattern := range kernelWarnPatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
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

func NewConsoleLogPipe(stopCh chan<- types.StopRequest, healthCheckCh chan<- struct{}) (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	kernelPrefix := color.New(color.FgMagenta, color.Bold).Sprint("👾 kernel | ")
	consolePrefix := color.New(color.FgYellow, color.Bold).Sprint("🐧 system | ")
	magenta := color.New(color.FgMagenta).SprintFunc()
	yellow := color.New(color.FgYellow).SprintFunc()

	panicRecorder := &KernelLogRecorder{}
	warnRecorder := &KernelLogRecorder{}
	kernelLogWriter := io.MultiWriter(panicRecorder, warnRecorder)

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
				// also shut down on eth0 (main interface) tx timeout. it won't recover, everything frozen
				if strings.Contains(line, "] Kernel panic - not syncing:") {
					// record panic log
					panicRecorder.Start(panicShutdownDelay, func(output string) {
						// report panic lines to sentry
						// if possible we read the last lines of the log file
						panicLog := output
						if logHistory, err := tryReadLogHistory(conf.VmgrLog(), panicLogLines); err == nil {
							panicLog = logHistory
						}

						var err error
						// no new line - sentry preview only shows first line
						msg := errors.New(panicLog)
						// we want to know about the rate of data corruption errors, but don't let it pollute real panics
						if strings.Contains(panicLog, "DATA IS LIKELY CORRUPTED") || strings.Contains(panicLog, "MissingDataPartition") {
							err = &DataCorruptionError{Err: msg}
							stopCh <- types.StopRequest{Type: types.StopTypeForce, Reason: types.StopReasonDataCorruption}
						} else {
							err = &KernelPanicError{Err: msg}
							stopCh <- types.StopRequest{Type: types.StopTypeForce, Reason: types.StopReasonKernelPanic}
						}
						sentry.CaptureException(err)
					})
				} else if matchWarnPattern(line) {
					// record warning log
					warnRecorder.Start(kernelWarnRecordDuration, func(output string) {
						sentry.CaptureException(&KernelWarning{
							Err: errors.New(output),
						})
					})

					// trigger a health check in case of RCU stall / netdev watchdog
					select {
					case healthCheckCh <- struct{}{}:
					default:
					}
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
							URL:     appid.UrlSettings,
							Silent:  true,
						})
						if err != nil {
							logrus.WithError(err).Error("failed to send OOM kill notification")
						}
					}()
				}

				// continue to write the log
				_, _ = io.WriteString(os.Stdout, kernelPrefix+magenta(line))
				_, _ = io.WriteString(kernelLogWriter, line)
			} else {
				_, _ = io.WriteString(os.Stdout, consolePrefix+yellow(line))
			}
		}
	}()

	return w, nil
}
