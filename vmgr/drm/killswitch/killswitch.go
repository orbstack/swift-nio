package killswitch

import (
	"errors"
	"time"
)

//go:generate ./gen.sh
const (
	checkInterval = 4 * time.Hour
)

var (
	// not idiomatic Go, but helpful for users
	friendlyErrMsg = "Update required: This beta version of OrbStack is too old. Please update to continue using OrbStack: https://orbstack.dev/download"
	ErrKillswitch  = errors.New(friendlyErrMsg)
	ExpiryTime     = time.Unix(killswitchTimestamp, 0)
)

func Check() error {
	// TODO restore this for beta
	return nil

	// now := time.Now().Unix()
	// if now > killswitchTimestamp {
	// 	return ErrKillswitch
	// }

	// return nil
}

func MonitorAndExit() error {
	// TODO restore this for beta
	return nil

	// // initial check
	// err := Check()
	// if err != nil {
	// 	return err
	// }

	// go func() {
	// 	err := WaitForExpiry()
	// 	if err != nil {
	// 		fmt.Fprintln(os.Stderr, err)
	// 		os.Exit(1)
	// 	}
	// }()

	// return nil
}

func WaitForExpiry() error {
	// TODO restore this for beta
	return nil
	// ticker := time.NewTicker(checkInterval)
	// defer ticker.Stop()

	// for range ticker.C {
	// 	err := Check()
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	// return nil
}

func Watch(fn func(error)) {
	// TODO restore this for beta
	return
	// go func() {
	// 	err := WaitForExpiry()
	// 	if err != nil {
	// 		fn(err)
	// 	}
	// }()
}
