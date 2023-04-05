package main

import (
	"bufio"
	"io"
	"os"
)

func NewConsoleLogPipe() (*os.File, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}

	go func() {
		defer w.Close()
		// copy each line and prefix it
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			io.WriteString(os.Stdout, "vm | "+scanner.Text()+"\n")
		}
	}()

	return w, nil
}
