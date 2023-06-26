//go:build !darwin

package iokit

var LastWakeTime *timex.MonoSleepTime

type SleepWakeMonitor struct {
	SleepChan chan time.Time
	WakeChan  chan time.Time
}

func MonitorSleepWake() (*SleepWakeMonitor, error) {
	return nil, errors.New("not implemented")
}

func IsAsleep() bool {
	return false
}
