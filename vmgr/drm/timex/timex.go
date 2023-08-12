package timex

import (
	"time"

	"golang.org/x/sys/unix"
)

type MonoSleepTime struct {
	mono int64
	Wall time.Time
}

func nowNs() int64 {
	var ts unix.Timespec
	// this clock is affected by NTP frequency slew,
	// but not by time jumps
	unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts)
	return ts.Nano()
}

func NowMonoSleep() MonoSleepTime {
	return MonoSleepTime{
		mono: nowNs(),
		// this includes a non-sleep mono reading too
		Wall: time.Now(),
	}
}

func SinceMonoSleep(t MonoSleepTime) time.Duration {
	// in case there is no mono time, use wall
	// happens when restored from keychain
	if t.mono == 0 {
		return time.Since(t.Wall)
	}

	return time.Duration(nowNs() - t.mono)
}
