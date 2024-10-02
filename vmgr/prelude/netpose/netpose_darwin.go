//go:build darwin

/*
 * To use this package: import _ "github.com/orbstack/macvirt/netpose"
 *
 * All Go and Rust binaries built for macOS should have this linked in as a workaround for
 * the XNU bug explained in netpose_static.c.
 */

package netpose

// this is in a separate package only imported on darwin,
// because .c files aren't allowed if Cgo isn't used in the same package
import _ "github.com/orbstack/macvirt/vmgr/prelude/netpose/internal"
