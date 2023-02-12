package killswitch

import (
	"errors"
	"time"
)

//go:generate ./gen.sh
const (
	checkInterval = 2 * time.Hour
)

var (
	ErrKillswitch = errors.New("build expired")
)

func doCheck() error {
	now := time.Now().Unix()
	if now > killswitchTimestamp {
		return ErrKillswitch
	}

	return nil
}

func Monitor() error {
	// initial check
	err := doCheck()
	if err != nil {
		return err
	}

	go func() {
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for range ticker.C {
			err := doCheck()
			if err != nil {
				panic(err)
			}
		}
	}()

	return nil
}
