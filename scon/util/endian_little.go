//go:build 386 || amd64 || amd64p32 || alpha || arm || arm64 || loong64 || mipsle || mips64le || mips64p32le || nios2 || ppc64le || riscv || riscv64 || sh || wasm

package util

import "math/bits"

func SwapNetHost16(x uint16) uint16 {
	return bits.ReverseBytes16(x)
}

func SwapNetHost32(x uint32) uint32 {
	return bits.ReverseBytes32(x)
}

func SwapNetHost64(x uint64) uint64 {
	return bits.ReverseBytes64(x)
}
