package cmd

import (
	"encoding/binary"
	"io"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func mFlag[T uint64 | uint32](n T) byte {
	if n != 0 {
		return 1
	}
	return 0
}

func WriteWindowSize(writer io.Writer) error {
	w, h, err := term.GetSize(0)
	if err != nil {
		return err
	}

	if err := binary.Write(writer, binary.BigEndian, uint32(w)); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(h)); err != nil {
		return err
	}
	return nil
}

func WriteTermiosState(writer io.Writer, termios *unix.Termios) error {

	buf, err := makeTermiosBuf(termios)
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

func makeTermiosBuf(termios *unix.Termios) ([]byte, error) {
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
		unix.CS5,
		unix.CS6,
		unix.CS7,
		unix.CS8,
		unix.PARENB,
		unix.PARODD,
	}

	// debugFile, err := os.Create("tmp2.txt")
	// defer debugFile.Close()
	// fmt.Fprintf(debugFile, "control chars: %+v\n", termios.Cc)
	// fmt.Fprintf(debugFile, "interrupt: %+v\n", unix.VINTR)
	// fmt.Fprintf(debugFile, "input flag: %+v\n", termios.Iflag)

	var buf []byte

	for _, cc := range control_chars {
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
			// fmt.Fprintf(debugFile, "flag: %+v, mask %+v, result %+v\n", item.host_flag, mask, mFlag(item.host_flag&mask))
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
