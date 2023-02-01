package util

import "net"

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
