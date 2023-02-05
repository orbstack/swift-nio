//go:build !darwin

package iokit

type SleepWakeMonitor struct {
	SleepChan chan time.Time
	WakeChan  chan time.Time
}

func MonitorSleepWake() (*SleepWakeMonitor, error) {
	return nil, errors.New("not implemented")
}
