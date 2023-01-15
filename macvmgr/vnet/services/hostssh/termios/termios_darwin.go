//go:build darwin

package termios

import (
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

func TermiosToSSH(t *unix.Termios) ssh.TerminalModes {
	m := make(ssh.TerminalModes)

	// cc
	m[ssh.VINTR] = uint32(t.Cc[unix.VINTR])
	m[ssh.VQUIT] = uint32(t.Cc[unix.VQUIT])
	m[ssh.VERASE] = uint32(t.Cc[unix.VERASE])
	m[ssh.VKILL] = uint32(t.Cc[unix.VKILL])
	m[ssh.VEOF] = uint32(t.Cc[unix.VEOF])
	m[ssh.VEOL] = uint32(t.Cc[unix.VEOL])
	m[ssh.VEOL2] = uint32(t.Cc[unix.VEOL2])
	m[ssh.VSTART] = uint32(t.Cc[unix.VSTART])
	m[ssh.VSTOP] = uint32(t.Cc[unix.VSTOP])
	m[ssh.VSUSP] = uint32(t.Cc[unix.VSUSP])
	//m[ssh.VDSUSP] = uint32(t.Cc[unix.VDSUSP])
	m[ssh.VREPRINT] = uint32(t.Cc[unix.VREPRINT])
	m[ssh.VWERASE] = uint32(t.Cc[unix.VWERASE])
	m[ssh.VLNEXT] = uint32(t.Cc[unix.VLNEXT])
	//m[ssh.VFLUSH] = uint32(t.Cc[unix.VFLUSH])
	//m[ssh.VSWTCH] = uint32(t.Cc[unix.VSWTCH])
	//m[ssh.VSTATUS] = uint32(t.Cc[unix.VSTATUS])
	m[ssh.VDISCARD] = uint32(t.Cc[unix.VDISCARD])

	// iflag
	m[ssh.IGNPAR] = mFlag(t.Iflag & unix.IGNPAR)
	m[ssh.PARMRK] = mFlag(t.Iflag & unix.PARMRK)
	m[ssh.INPCK] = mFlag(t.Iflag & unix.INPCK)
	m[ssh.ISTRIP] = mFlag(t.Iflag & unix.ISTRIP)
	m[ssh.INLCR] = mFlag(t.Iflag & unix.INLCR)
	m[ssh.IGNCR] = mFlag(t.Iflag & unix.IGNCR)
	m[ssh.ICRNL] = mFlag(t.Iflag & unix.ICRNL)
	//m[ssh.IUCLC] = mFlag(t.Iflag & unix.IUCLC)
	m[ssh.IXON] = mFlag(t.Iflag & unix.IXON)
	m[ssh.IXANY] = mFlag(t.Iflag & unix.IXANY)
	m[ssh.IXOFF] = mFlag(t.Iflag & unix.IXOFF)
	m[ssh.IMAXBEL] = mFlag(t.Iflag & unix.IMAXBEL)
	m[ssh.IUTF8] = mFlag(t.Iflag & unix.IUTF8)

	// lflag
	m[ssh.ISIG] = mFlag(t.Lflag & unix.ISIG)
	m[ssh.ICANON] = mFlag(t.Lflag & unix.ICANON)
	//m[ssh.XCASE] = mFlag(t.Lflag & unix.XCASE)
	m[ssh.ECHO] = mFlag(t.Lflag & unix.ECHO)
	m[ssh.ECHOE] = mFlag(t.Lflag & unix.ECHOE)
	m[ssh.ECHOK] = mFlag(t.Lflag & unix.ECHOK)
	m[ssh.ECHONL] = mFlag(t.Lflag & unix.ECHONL)
	m[ssh.NOFLSH] = mFlag(t.Lflag & unix.NOFLSH)
	m[ssh.TOSTOP] = mFlag(t.Lflag & unix.TOSTOP)
	m[ssh.IEXTEN] = mFlag(t.Lflag & unix.IEXTEN)
	m[ssh.ECHOCTL] = mFlag(t.Lflag & unix.ECHOCTL)
	m[ssh.ECHOKE] = mFlag(t.Lflag & unix.ECHOKE)
	m[ssh.PENDIN] = mFlag(t.Lflag & unix.PENDIN)

	// oflag
	m[ssh.OPOST] = mFlag(t.Oflag & unix.OPOST)
	//m[ssh.OLCUC] = mFlag(t.Oflag & unix.OLCUC)
	m[ssh.ONLCR] = mFlag(t.Oflag & unix.ONLCR)
	m[ssh.OCRNL] = mFlag(t.Oflag & unix.OCRNL)
	m[ssh.ONOCR] = mFlag(t.Oflag & unix.ONOCR)
	m[ssh.ONLRET] = mFlag(t.Oflag & unix.ONLRET)

	// cflag
	m[ssh.CS7] = mFlag(t.Cflag & unix.CS7)
	m[ssh.CS8] = mFlag(t.Cflag & unix.CS8)
	m[ssh.PARENB] = mFlag(t.Cflag & unix.PARENB)
	m[ssh.PARODD] = mFlag(t.Cflag & unix.PARODD)

	// ispeed
	m[ssh.TTY_OP_ISPEED] = uint32(t.Ispeed)
	// ospeed
	m[ssh.TTY_OP_OSPEED] = uint32(t.Ospeed)

	return m
}

func ApplySSHToTermios(m ssh.TerminalModes, t *unix.Termios) {
	for op, val := range m {
		switch op {
		// speed
		case ssh.TTY_OP_ISPEED:
			t.Ispeed = uint64(val)
		case ssh.TTY_OP_OSPEED:
			t.Ospeed = uint64(val)

		// cc
		case ssh.VINTR:
			t.Cc[unix.VINTR] = uint8(val)
		case ssh.VQUIT:
			t.Cc[unix.VQUIT] = uint8(val)
		case ssh.VERASE:
			t.Cc[unix.VERASE] = uint8(val)
		case ssh.VKILL:
			t.Cc[unix.VKILL] = uint8(val)
		case ssh.VEOF:
			t.Cc[unix.VEOF] = uint8(val)
		case ssh.VEOL:
			t.Cc[unix.VEOL] = uint8(val)
		case ssh.VEOL2:
			t.Cc[unix.VEOL2] = uint8(val)
		case ssh.VSTART:
			t.Cc[unix.VSTART] = uint8(val)
		case ssh.VSTOP:
			t.Cc[unix.VSTOP] = uint8(val)
		case ssh.VSUSP:
			t.Cc[unix.VSUSP] = uint8(val)
		//case ssh.VDSUSP:
		case ssh.VREPRINT:
			t.Cc[unix.VREPRINT] = uint8(val)
		case ssh.VWERASE:
			t.Cc[unix.VWERASE] = uint8(val)
		case ssh.VLNEXT:
			t.Cc[unix.VLNEXT] = uint8(val)
		//case ssh.VSTATUS:
		case ssh.VDISCARD:
			t.Cc[unix.VDISCARD] = uint8(val)

		// iflag
		case ssh.IGNPAR:
			if val != 0 {
				t.Iflag |= unix.IGNPAR
			} else {
				t.Iflag &^= unix.IGNPAR
			}
		case ssh.PARMRK:
			if val != 0 {
				t.Iflag |= unix.PARMRK
			} else {
				t.Iflag &^= unix.PARMRK
			}
		case ssh.INPCK:
			if val != 0 {
				t.Iflag |= unix.INPCK
			} else {
				t.Iflag &^= unix.INPCK
			}
		case ssh.ISTRIP:
			if val != 0 {
				t.Iflag |= unix.ISTRIP
			} else {
				t.Iflag &^= unix.ISTRIP
			}
		case ssh.INLCR:
			if val != 0 {
				t.Iflag |= unix.INLCR
			} else {
				t.Iflag &^= unix.INLCR
			}
		case ssh.IGNCR:
			if val != 0 {
				t.Iflag |= unix.IGNCR
			} else {
				t.Iflag &^= unix.IGNCR
			}
		case ssh.ICRNL:
			if val != 0 {
				t.Iflag |= unix.ICRNL
			} else {
				t.Iflag &^= unix.ICRNL
			}
		case ssh.IXON:
			if val != 0 {
				t.Iflag |= unix.IXON
			} else {
				t.Iflag &^= unix.IXON
			}
		case ssh.IXANY:
			if val != 0 {
				t.Iflag |= unix.IXANY
			} else {
				t.Iflag &^= unix.IXANY
			}
		case ssh.IXOFF:
			if val != 0 {
				t.Iflag |= unix.IXOFF
			} else {
				t.Iflag &^= unix.IXOFF
			}
		case ssh.IMAXBEL:
			if val != 0 {
				t.Iflag |= unix.IMAXBEL
			} else {
				t.Iflag &^= unix.IMAXBEL
			}
		case ssh.IUTF8:
			if val != 0 {
				t.Iflag |= unix.IUTF8
			} else {
				t.Iflag &^= unix.IUTF8
			}

		// lflag
		case ssh.ISIG:
			if val != 0 {
				t.Lflag |= unix.ISIG
			} else {
				t.Lflag &^= unix.ISIG
			}
		case ssh.ICANON:
			if val != 0 {
				t.Lflag |= unix.ICANON
			} else {
				t.Lflag &^= unix.ICANON
			}
		case ssh.ECHO:
			if val != 0 {
				t.Lflag |= unix.ECHO
			} else {
				t.Lflag &^= unix.ECHO
			}
		case ssh.ECHOE:
			if val != 0 {
				t.Lflag |= unix.ECHOE
			} else {
				t.Lflag &^= unix.ECHOE
			}
		case ssh.ECHOK:
			if val != 0 {
				t.Lflag |= unix.ECHOK
			} else {
				t.Lflag &^= unix.ECHOK
			}
		case ssh.ECHONL:
			if val != 0 {
				t.Lflag |= unix.ECHONL
			} else {
				t.Lflag &^= unix.ECHONL
			}
		case ssh.NOFLSH:
			if val != 0 {
				t.Lflag |= unix.NOFLSH
			} else {
				t.Lflag &^= unix.NOFLSH
			}
		case ssh.TOSTOP:
			if val != 0 {
				t.Lflag |= unix.TOSTOP
			} else {
				t.Lflag &^= unix.TOSTOP
			}
		case ssh.IEXTEN:
			if val != 0 {
				t.Lflag |= unix.IEXTEN
			} else {
				t.Lflag &^= unix.IEXTEN
			}
		case ssh.ECHOCTL:
			if val != 0 {
				t.Lflag |= unix.ECHOCTL
			} else {
				t.Lflag &^= unix.ECHOCTL
			}
		case ssh.ECHOKE:
			if val != 0 {
				t.Lflag |= unix.ECHOKE
			} else {
				t.Lflag &^= unix.ECHOKE
			}
		case ssh.PENDIN:
			if val != 0 {
				t.Lflag |= unix.PENDIN
			} else {
				t.Lflag &^= unix.PENDIN
			}

		// oflag
		case ssh.OPOST:
			if val != 0 {
				t.Oflag |= unix.OPOST
			} else {
				t.Oflag &^= unix.OPOST
			}
		case ssh.ONLCR:
			if val != 0 {
				t.Oflag |= unix.ONLCR
			} else {
				t.Oflag &^= unix.ONLCR
			}
		case ssh.OCRNL:
			if val != 0 {
				t.Oflag |= unix.OCRNL
			} else {
				t.Oflag &^= unix.OCRNL
			}
		case ssh.ONOCR:
			if val != 0 {
				t.Oflag |= unix.ONOCR
			} else {
				t.Oflag &^= unix.ONOCR
			}
		case ssh.ONLRET:
			if val != 0 {
				t.Oflag |= unix.ONLRET
			} else {
				t.Oflag &^= unix.ONLRET
			}

		// cflag
		case ssh.CS7:
			if val != 0 {
				t.Cflag |= unix.CS7
			} else {
				t.Cflag &^= unix.CS7
			}
		case ssh.CS8:
			if val != 0 {
				t.Cflag |= unix.CS8
			} else {
				t.Cflag &^= unix.CS8
			}
		case ssh.PARENB:
			if val != 0 {
				t.Cflag |= unix.PARENB
			} else {
				t.Cflag &^= unix.PARENB
			}
		case ssh.PARODD:
			if val != 0 {
				t.Cflag |= unix.PARODD
			} else {
				t.Cflag &^= unix.PARODD
			}
		}
	}
}

func GetTermios(fd uintptr) (*unix.Termios, error) {
	return unix.IoctlGetTermios(int(fd), unix.TIOCGETA)
}

func SetTermiosNow(fd uintptr, t *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), unix.TIOCSETA, t)
}

func SetTermiosDrain(fd uintptr, t *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), unix.TIOCSETAW, t)
}
