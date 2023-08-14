package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func main() {
	for _, arg := range os.Args[1:] {
		fh, mountID, err := unix.NameToHandleAt(unix.AT_FDCWD, arg, unix.AT_SYMLINK_FOLLOW)
		if err != nil {
			panic(err)
		}

		fmt.Println("Path: ", arg)
		fmt.Println("  Mount ID:", mountID)
		fmt.Println("  File handle:")
		fmt.Println("    Type: ", fh.Type())
		fmt.Println("    Size: ", fh.Size())
		fmt.Println("    Bytes: ", fh.Bytes())
		fmt.Println("    (hex): ", hex.EncodeToString(fh.Bytes()))
		fmt.Println("    (b64): ", base64.StdEncoding.EncodeToString(fh.Bytes()))
		fmt.Println()
	}
}
