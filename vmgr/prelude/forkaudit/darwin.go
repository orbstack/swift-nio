//go:build darwin && !release

package forkaudit

// this is in a separate package only imported on darwin,
// because .c files aren't allowed if Cgo isn't used in the same package
import _ "github.com/orbstack/macvirt/vmgr/prelude/forkaudit/internal"
