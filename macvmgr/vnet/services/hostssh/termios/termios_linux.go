//go:build linux

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
	m[ssh.IUCLC] = mFlag(t.Iflag & unix.IUCLC)
	m[ssh.IXON] = mFlag(t.Iflag & unix.IXON)
	m[ssh.IXANY] = mFlag(t.Iflag & unix.IXANY)
	m[ssh.IXOFF] = mFlag(t.Iflag & unix.IXOFF)
	m[ssh.IMAXBEL] = mFlag(t.Iflag & unix.IMAXBEL)
	m[ssh.IUTF8] = mFlag(t.Iflag & unix.IUTF8)

	// lflag
	m[ssh.ISIG] = mFlag(t.Lflag & unix.ISIG)
	m[ssh.ICANON] = mFlag(t.Lflag & unix.ICANON)
	m[ssh.XCASE] = mFlag(t.Lflag & unix.XCASE)
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
	m[ssh.OLCUC] = mFlag(t.Oflag & unix.OLCUC)
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

func GetTermios(fd uintptr) (*unix.Termios, error) {
	return unix.IoctlGetTermios(int(fd), unix.TCGETS2)
}

func SetTermiosNow(fd uintptr, t *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), unix.TCSETS2, t)
}

func SetTermiosDrain(fd uintptr, t *unix.Termios) error {
	return unix.IoctlSetTermios(int(fd), unix.TCSETSW2, t)
}
