package sysx

// #include <stdlib.h>
import "C"

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

func Swapoff(path string) error {
	// null-terminated string
	cStr := C.CString(path)
	defer C.free(unsafe.Pointer(cStr))

	_, _, errno := unix.Syscall(unix.SYS_SWAPOFF, uintptr(unsafe.Pointer(cStr)), 0, 0)
	if errno != 0 {
		return errno
	}

	return nil
}
