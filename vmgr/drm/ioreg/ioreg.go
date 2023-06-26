package ioreg

/*
#cgo LDFLAGS: -framework Foundation -framework IOKit

#include "ioreg.h"
*/
import "C"
import (
	"errors"
	"unsafe"
)

var (
	ErrIoreg = errors.New("ioreg error")
)

func GetPlatformUUID() (string, error) {
	cstr := C.ReadPlatformUUID()
	if cstr == nil {
		return "", ErrIoreg
	}
	gostr := C.GoString(cstr)
	C.free(unsafe.Pointer(cstr))
	return gostr, nil
}

func GetSerialNumber() (string, error) {
	cstr := C.ReadSerialNumber()
	if cstr == nil {
		return "", ErrIoreg
	}
	gostr := C.GoString(cstr)
	C.free(unsafe.Pointer(cstr))
	return gostr, nil
}

func GetMacAddress() (string, error) {
	cstr := C.ReadMacAddress()
	if cstr == nil {
		return "", ErrIoreg
	}
	gostr := C.GoString(cstr)
	C.free(unsafe.Pointer(cstr))
	return gostr, nil
}
