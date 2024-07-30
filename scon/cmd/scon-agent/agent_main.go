package main

import (
	"unsafe"

	"github.com/orbstack/macvirt/scon/agent"
	"golang.org/x/sys/unix"
)

func init() {
	setProcessComm(agent.ProcessName)
}

func setProcessComm(name string) error {
	bytePtr, err := unix.BytePtrFromString(name)
	if err != nil {
		return err
	}

	if _, _, errno := unix.RawSyscall6(unix.SYS_PRCTL, unix.PR_SET_NAME, uintptr(unsafe.Pointer(bytePtr)), 0, 0, 0, 0); errno != 0 {
		return errno
	}
	return nil
}

func main() {
	agent.Main()
}
