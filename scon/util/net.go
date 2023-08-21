package util

import (
	"net"
	"os"
)

func DefaultAddress4() net.IP {
	conn, err := net.Dial("udp", "8.8.4.4:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To4()
}

func DefaultAddress6() net.IP {
	conn, err := net.Dial("udp", "[2606:4700:4700::1001]:33000")
	if err != nil {
		return nil
	}
	defer conn.Close()
	return conn.LocalAddr().(*net.UDPAddr).IP.To16()
}

func ListenUnixWithPerms(path string, perms os.FileMode, uid, gid int) (net.Listener, error) {
	_ = os.Remove(path)

	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}

	err = os.Chmod(path, perms)
	if err != nil {
		return nil, err
	}

	err = os.Chown(path, uid, gid)
	if err != nil {
		return nil, err
	}

	return listener, nil
}
