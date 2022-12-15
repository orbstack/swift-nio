package main

import (
	"os"
	"syscall"

	"github.com/pkg/term/termios"
	"golang.org/x/sys/unix"
)

// https://developer.apple.com/documentation/virtualization/running_linux_in_a_virtual_machine?language=objc#:~:text=Configure%20the%20Serial%20Port%20Device%20for%20Standard%20In%20and%20Out
func setRawMode(f *os.File) *unix.Termios {
	var oldAttr unix.Termios
	var attr unix.Termios

	// Get settings for terminal
	// this still isn't raw mode, still converts ^C, so we set new one
	termios.Tcgetattr(f.Fd(), &oldAttr)

	// Put stdin into raw mode, disabling local echo, input canonicalization,
	// and CR-NL mapping.
	attr.Iflag &^= syscall.ICRNL
	attr.Lflag &^= syscall.ICANON | syscall.ECHO

	// Set minimum characters when reading = 1 char
	attr.Cc[syscall.VMIN] = 1

	// set timeout when reading as non-canonical mode
	attr.Cc[syscall.VTIME] = 0

	// reflects the changed settings
	termios.Tcsetattr(f.Fd(), termios.TCSANOW, &attr)

	return &oldAttr
}

func revertRawMode(f *os.File, oldAttr *unix.Termios) {
	termios.Tcsetattr(f.Fd(), termios.TCSANOW, oldAttr)
}
