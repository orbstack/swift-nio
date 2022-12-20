package network

import (
	"os"
	"os/exec"
	"strconv"

	"golang.org/x/sys/unix"
)

func StartVpnkitPair() (*os.File, error) {
	file0, fd1, err := makeUnixDgramPair()
	if err != nil {
		return nil, err
	}

	// unset CLOEXEC
	unix.FcntlInt(uintptr(fd1), unix.F_SETFD, 0)

	// spawn process: Contents/Resources/bin/com.docker.vpnkit --ethernet fd:2 --mtu 65520 --debug
	cmd := exec.Command("/Applications/Docker.app/Contents/Resources/bin/com.docker.vpnkit", "--ethernet", "fd:"+strconv.Itoa(fd1), "--mtu", "65520", "--debug")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	go cmd.Run()

	return file0, nil
}
