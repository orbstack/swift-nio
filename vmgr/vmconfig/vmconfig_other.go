//go:build !darwin

package vmconfig

import "sync"

func validateAPFS(dataDir string) error {
	return nil
}

var IsAdmin = sync.OnceValue(func() bool {
	return false
})
