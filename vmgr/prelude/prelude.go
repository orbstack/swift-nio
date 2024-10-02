/*
 * Global Go and OS bug fixes/workarounds that should be linked into all OrbStack binaries.
 * To use: import _ "github.com/orbstack/macvirt/vmgr/prelude"
 */

package prelude

// Link netpose into all macOS binaries.
// See netpose docs for why this is necessary.
// This package is a no-op on other platforms.
import (
	"fmt"
	"os"

	_ "github.com/orbstack/macvirt/vmgr/prelude/netpose"
)

func init() {
	// enable GODEBUG=x509negativeserial=1
	// some MITM proxies generate invalid certs, which Go 1.23 complains about: https://github.com/orbstack/orbstack/issues/1490
	godebug, _ := os.LookupEnv("GODEBUG")
	if godebug != "" {
		godebug += ","
	}
	godebug += "x509negativeserial=1"
	err := os.Setenv("GODEBUG", godebug)
	if err != nil {
		panic(fmt.Errorf("prelude setenv: %w", err))
	}
}
