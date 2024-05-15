//go:build !darwin

package pspawn

import (
	"context"
	"os/exec"
)

type Error = exec.Error
type ExitError = exec.ExitError
type Cmd = exec.Cmd

var ErrDot = exec.ErrDot
var ErrNotFound = exec.ErrNotFound
var ErrWaitDelay = exec.ErrWaitDelay

func Command(name string, arg ...string) *Cmd {
	return exec.Command(name, arg...)
}

func CommandContext(ctx context.Context, name string, arg ...string) *Cmd {
	return exec.CommandContext(ctx, name, arg...)
}
