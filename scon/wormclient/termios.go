package wormclient

import (
	"encoding/binary"

	"golang.org/x/sys/unix"
)

var controlChars = [...]int{
	unix.VINTR,
	unix.VQUIT,
	unix.VERASE,
	unix.VKILL,
	unix.VEOF,
	unix.VEOL,
	unix.VEOL2,
	unix.VSTART,
	unix.VSTOP,
	unix.VSUSP,
	// unix.VDSUSP,
	unix.VREPRINT,
	unix.VWERASE,
	unix.VLNEXT,
	// unix.VFLUSH,
	// unix.VSWTCH,
	// unix.VSTATUS,
	unix.VDISCARD,
}
var inputFlags = []uint64{
	unix.IGNPAR,
	unix.PARMRK,
	unix.INPCK,
	unix.ISTRIP,
	unix.INLCR,
	unix.IGNCR,
	unix.ICRNL,
	// unix.IUCLC,
	unix.IXON,
	unix.IXANY,
	unix.IXOFF,
	unix.IMAXBEL,
	unix.IUTF8,
}

var localFlags = []uint64{
	unix.ISIG,
	unix.ICANON,
	// unix.XCASE,
	unix.ECHO,
	unix.ECHOE,
	unix.ECHOK,
	unix.ECHONL,
	unix.NOFLSH,
	unix.TOSTOP,
	unix.IEXTEN,
	unix.ECHOCTL,
	unix.ECHOKE,
	unix.PENDIN,
}

var outputFlags = []uint64{
	unix.OPOST,
	// unix.OLCUC,
	unix.ONLCR,
	unix.OCRNL,
	unix.ONOCR,
	unix.ONLRET,
}

var controlFlags = []uint64{
	unix.CS5,
	unix.CS6,
	unix.CS7,
	unix.CS8,
	unix.PARENB,
	unix.PARODD,
}

func mFlag[T uint64 | uint32](n T) byte {
	if n != 0 {
		return 1
	}
	return 0
}

func SerializeTermios(termios *unix.Termios) ([]byte, error) {
	var buf []byte
	for _, cc := range controlChars {
		buf = append(buf, termios.Cc[cc])
	}
	for _, mask := range inputFlags {
		buf = append(buf, mFlag(termios.Iflag&mask))
	}
	for _, mask := range localFlags {
		buf = append(buf, mFlag(termios.Lflag&mask))
	}
	for _, mask := range outputFlags {
		buf = append(buf, mFlag(termios.Oflag&mask))
	}
	for _, mask := range controlFlags {
		buf = append(buf, mFlag(termios.Cflag&mask))
	}

	speedBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(speedBuf, uint32(termios.Ispeed))
	buf = append(buf, speedBuf...)

	binary.BigEndian.PutUint32(speedBuf, uint32(termios.Ospeed))
	buf = append(buf, speedBuf...)
	return buf, nil

}
