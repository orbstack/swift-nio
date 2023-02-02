package main

import (
	"errors"
	"io"
	"os"

	"github.com/pkg/sftp"
)

func main() {
	// stdin fd should be a socket
	// stdout and stderr are left alone for output
	server, err := sftp.NewServer(os.Stdin)
	if err != nil {
		panic(err)
	}

	err = server.Serve()
	if err != nil && !errors.Is(err, io.EOF) {
		panic(err)
	}
}
