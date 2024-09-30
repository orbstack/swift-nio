package cmd

import (
	"encoding/binary"
	"io"

	"golang.org/x/sys/unix"
)

func mFlag[T uint64 | uint32](n T) byte {
	if n != 0 {
		return 1
	}
	return 0
}

func WriteTermiosState(writer io.Writer) error {
	buf, err := makeTermiosBuf()
	if err != nil {
		return err
	}

	if err := binary.Write(writer, binary.BigEndian, uint32(len(buf))); err != nil {
		return err
	}
	_, err = writer.Write(buf)
	if err != nil {
		return err
	}
	return nil
}

func makeTermiosBuf() ([]byte, error) {
	var buf []byte

	termios, err := unix.IoctlGetTermios(0, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	var control_chars = [...]int{
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
		// unix.CS5,
		// unix.CS6,
		unix.CS7,
		unix.CS8,
		unix.PARENB,
		unix.PARODD,
	}

	for cc := range control_chars {
		buf = append(buf, termios.Cc[cc])
	}

	// todo: fix uint32 / uint64 behaviour
	flags := []struct {
		host_flag uint64
		masks     []uint64
	}{
		{uint64(termios.Iflag), inputFlags},
		{uint64(termios.Lflag), localFlags},
		{uint64(termios.Oflag), outputFlags},
		{uint64(termios.Cflag), controlFlags},
	}

	for _, item := range flags {
		for _, mask := range item.masks {
			buf = append(buf, mFlag(item.host_flag&mask))
		}
	}

	speedBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(speedBuf, uint32(termios.Ispeed))
	buf = append(buf, speedBuf...)

	binary.BigEndian.PutUint32(speedBuf, uint32(termios.Ospeed))
	buf = append(buf, speedBuf...)
	return buf, nil

}
