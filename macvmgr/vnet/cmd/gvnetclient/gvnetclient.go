//go:build linux

package main

import (
	"encoding/binary"
	"os"
	"runtime"

	"github.com/mdlayher/vsock"
	"github.com/songgao/water"
	"golang.org/x/sys/unix"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	config := water.Config{
		DeviceType: water.TAP,
		PlatformSpecificParams: water.PlatformSpecificParams{
			Name: "tap0",
		},
	}
	ifce, err := water.New(config)
	check(err)
	//defer ifce.Close()
	tapFile := ifce.ReadWriteCloser.(*os.File)
	rawConn, err := tapFile.SyscallConn()
	check(err)
	err = rawConn.Control(func(fd uintptr) {
		err = unix.IoctlSetInt(int(fd), unix.TUNSETOFFLOAD, 0x01)
		check(err)
	})
	check(err)

	vsockListener, err := vsock.ListenContextID(4294967295, 100, &vsock.Config{})
	check(err)
	//defer vsockListener.Close()
	vsockConn, err := vsockListener.Accept()
	check(err)
	//defer vsockConn.Close()

	go func() {
		buf := make([]byte, 512*1024)
		lenBuf := make([]byte, 2)
		for {
			n, err := vsockConn.Read(lenBuf)
			check(err)
			if n != 2 {
				return
			}
			len := binary.LittleEndian.Uint16(lenBuf)
			n, err = vsockConn.Read(buf[:len])
			check(err)
			if n != int(len) {
				return
			}
			_, err = tapFile.Write(buf[:len])
			check(err)
		}
	}()

	go func() {
		buf := make([]byte, 512*1024)
		lenBuf := make([]byte, 2)
		for {
			n, err := tapFile.Read(buf)
			check(err)
			binary.LittleEndian.PutUint16(lenBuf, uint16(n))
			_, err = vsockConn.Write(lenBuf)
			check(err)
			_, err = vsockConn.Write(buf[:n])
			check(err)
		}
	}()

	runtime.Goexit()
}
