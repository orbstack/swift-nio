package main

import "github.com/Code-Hex/vz/v3"

const (
	portGvproxy = 101
)

func runVsockServices(device *vz.VirtioSocketDevice) error {
	gvproxyListener, err := device.Listen(portGvproxy)
	if err != nil {
		return err
	}
	//defer gvproxyListener.Close()
	go func() {
		for {
			_, err := gvproxyListener.Accept()
			if err != nil {
				return
			}
			//go handleGvproxyConn(conn)
		}
	}()

	return nil
}
