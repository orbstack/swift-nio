/*
 * cgostub: stub `main` package for -buildmode=c-archive
 *
 * Needed because:
 *   - buildmode=c-archive requires a `main` package, even though it never calls main()
 *   - By libc ABI, main() must return int. Cgo export names must be the same as Go function names,
       so Go's func main() can't be used because it returns void.
*/

package main

// import the real C main() implementation
import _ "github.com/orbstack/macvirt/vmgr/cgostub/mainfunc"

// never called
// main() must be in another non-main package to avoid name conflict
func main() {
}
