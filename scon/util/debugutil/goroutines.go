package debugutil

import (
	"fmt"
	"runtime"
)

func PrintGoroutines() {
	buf := make([]byte, 1048576)
	n := runtime.Stack(buf, true)
	fmt.Printf("\n------------CUT------------\n%s\n------------CUT------------\n", string(buf[:n]))
}
