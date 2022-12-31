//go:build !darwin

package iokit

func MonitorSleepWake() (chan time.Time, error) {
	return nil, errors.New("not implemented")
}
