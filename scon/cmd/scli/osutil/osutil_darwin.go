//go:build darwin

package osutil

import "golang.org/x/sys/unix"

func OsVersionCode() (string, error) {
	return unix.Sysctl("kern.osversion")
}

func OsProductVersion() (string, error) {
	return unix.Sysctl("kern.osproductversion")
}

func CpuModel() (string, error) {
	return unix.Sysctl("machdep.cpu.brand_string")
}

func MachineModel() (string, error) {
	return unix.Sysctl("hw.model")
}
