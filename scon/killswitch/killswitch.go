package killswitch

import (
	"errors"
	"fmt"
	"os"
	"time"
)

//go:generate ./gen.sh
const (
	checkInterval = 4 * time.Hour
)

var (
	// not idiomatic Go, but helpful for users
	friendlyErrMsg = "This beta build has expired. Please update to the latest version to continue using OrbStack: https://orbstack.dev/download"
	ErrKillswitch  = errors.New(friendlyErrMsg)
)

func Check() error {
	// TODO restore
	return nil
	now := time.Now().Unix()
	if now > killswitchTimestamp {
		return ErrKillswitch
	}

	return nil
}

func MonitorAndExit() error {
	// TODO restore
	return nil
	// initial check
	err := Check()
	if err != nil {
		return err
	}

	go func() {
		err := WaitForExpiry()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}()

	return nil
}

func WaitForExpiry() error {
	// TODO restore
	return nil
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		err := Check()
		if err != nil {
			return err
		}
	}

	return nil
}

func Watch(fn func(error)) {
	// TODO restore
	return
	go func() {
		err := WaitForExpiry()
		if err != nil {
			fn(err)
		}
	}()
}
