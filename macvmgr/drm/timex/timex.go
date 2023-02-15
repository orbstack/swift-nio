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
		Wall: time.Now().Truncate(-1),
	}
}

func SinceMonoSleep(t MonoSleepTime) time.Duration {
	return time.Duration(nowNs() - t.mono)
}
