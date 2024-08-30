//go:build darwin

/*
 * To use this package: import _ "github.com/orbstack/macvirt/netpose"
 *
 * All Go and Rust binaries built for macOS should have this linked in as a workaround for
 * the XNU bug explained in netpose_static.c.
 */

package netpose

// cgo automatically compiles and links *.c in the same dir, so no need to include anything here

// #cgo CFLAGS: -O2
import "C"
