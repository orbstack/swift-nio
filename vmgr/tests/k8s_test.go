package tests

import (
	"testing"
)

// can't be parallel. messes up docker tests
func TestK8sControlCLI(t *testing.T) {
	for _, action := range []string{
		// start k8s
		"start",
		// restart
		"restart",
		// stop k8s
		"stop",
		// restart when stopped
		"restart",
		// stop k8s
		"stop",
	} {
		_, err := runScli("orbctl", action, "k8s")
		checkT(t, err)
	}
}
