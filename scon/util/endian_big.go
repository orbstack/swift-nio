//go:build armbe || arm64be || m68k || mips || mips64 || mips64p32 || ppc || ppc64 || s390 || s390x || shbe || sparc || sparc64

package util

func SwapNetHost16(x uint16) uint16 {
	return x
}

func SwapNetHost32(x uint32) uint32 {
	return x
}

func SwapNetHost64(x uint64) uint64 {
	return x
}
