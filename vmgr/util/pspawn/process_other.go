//go:build !darwin

package pspawn

import "os"

func StartProcess(exe string, argv []string, attr *os.ProcAttr) (*os.Process, error) {
	return os.StartProcess(exe, argv, attr)
}
