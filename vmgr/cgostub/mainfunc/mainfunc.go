package mainfunc

import "C"

// import the rest of vmgr as a library
import "github.com/orbstack/macvirt/vmgr"

// This is the real C main() implementation.
// Cgo translates C.int to int, making the C compiler happy about main's return type. (Go int doesn't work because it's a typedef.)
//
//export main
func main() C.int {
	vmgr.Main()
	return 0
}
