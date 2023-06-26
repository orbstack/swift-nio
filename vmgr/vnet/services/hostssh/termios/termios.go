package termios

func mFlag[T uint64 | uint32](n T) uint32 {
	if n != 0 {
		return 1
	}
	return 0
}
