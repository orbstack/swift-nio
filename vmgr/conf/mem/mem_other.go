//go:build !darwin

package mem

func PhysicalMemory() uint64 {
	panic("unimplemented")
}
