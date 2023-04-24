package main

import (
	"net"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

var (
	msgToWrite = makeMsg()
)

func makeMsg() []byte {
	// 32768 bytes
	buf := make([]byte, 32768)
	for i := 0; i < len(buf); i++ {
		buf[i] = byte(i % 256)
	}
	return buf
}

func makeSocketpair() (int, error) {
	fds, err := unix.Socketpair(unix.AF_LOCAL, unix.SOCK_STREAM, 0)
	if err != nil {
		return 0, err
	}
	f2 := os.NewFile(uintptr(fds[1]), "socketpair")
	defer f2.Close()

	// make both nonblock
	if err := unix.SetNonblock(fds[0], true); err != nil {
		return 0, err
	}
	if err := unix.SetNonblock(fds[1], true); err != nil {
		return 0, err
	}

	c2, err := net.FileConn(f2)
	if err != nil {
		return 0, err
	}
	go func() {
		defer c2.Close()
		buf := make([]byte, 65536)
		for {
			_, err := c2.Read(buf)
			if err != nil {
				return
			}
		}
	}()
	return fds[0], nil
}

func makePipePair() (int, error) {
	var fds [2]int
	err := unix.Pipe(fds[:])
	if err != nil {
		return 0, err
	}

	// make both nonblock
	if err := unix.SetNonblock(fds[0], true); err != nil {
		return 0, err
	}
	if err := unix.SetNonblock(fds[1], true); err != nil {
		return 0, err
	}

	f2 := os.NewFile(uintptr(fds[1]), "pipe")
	go func() {
		defer f2.Close()
		buf := make([]byte, 65536)
		for {
			_, err := f2.Read(buf)
			if err != nil {
				return
			}
		}
	}()
	return fds[0], nil
}

func BenchmarkCgoGetpid(b *testing.B) {
	for i := 0; i < b.N; i++ {
		cgoGetpid()
	}
}

func BenchmarkGoGetpid(b *testing.B) {
	for i := 0; i < b.N; i++ {
		os.Getpid()
	}
}

func BenchmarkCgoDgramWrite(b *testing.B) {
	fd, err := makeSocketpair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cgoWrite(fd, msgToWrite)
	}
}

func BenchmarkGoDgramWrite(b *testing.B) {
	fd, err := makeSocketpair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		unix.Write(fd, msgToWrite)
	}
}

func BenchmarkGonetDgramWrite(b *testing.B) {
	fd, err := makeSocketpair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	c, err := net.FileConn(os.NewFile(uintptr(fd), "socketpair"))
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Write(msgToWrite)
	}
}

/*
	func BenchmarkCgoDgramWritev(b *testing.B) {
		fd, err := makeSocketpair()
		if err != nil {
			b.Fatal(err)
		}
		defer unix.Close(fd)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ptr := &msgToWrite[0]
			cgoWritev(fd, cgoIovec{
				iov_base: unsafe.Pointer(ptr), iov_len: cgoUlong(len(msgToWrite)),
			})
		}
	}

	func BenchmarkGonetDgramWritev(b *testing.B) {
		fd, err := makeSocketpair()
		if err != nil {
			b.Fatal(err)
		}
		defer unix.Close(fd)
		c, err := net.FileConn(os.NewFile(uintptr(fd), "socketpair"))
		if err != nil {
			b.Fatal(err)
		}
		defer c.Close()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			var bufs [1][]byte
			bufs[0] = msgToWrite
			netBufs := net.Buffers(bufs[:])
			netBufs.WriteTo(c)
		}
	}
*/
func BenchmarkCgoDgramSendmsg(b *testing.B) {
	fd, err := makeSocketpair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cgoSendmsg(fd, cgoIovec{iov_base: unsafe.Pointer(&msgToWrite[0]), iov_len: cgoUlong(len(msgToWrite))})
	}
}

func BenchmarkGoDgramSendmsg(b *testing.B) {
	fd, err := makeSocketpair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		unix.Sendmsg(fd, msgToWrite, nil, nil, 0)
	}
}

func BenchmarkGonetDgramSendmsg(b *testing.B) {
	fd, err := makeSocketpair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	c, err := net.FileConn(os.NewFile(uintptr(fd), "socketpair"))
	if err != nil {
		b.Fatal(err)
	}
	defer c.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.(*net.UnixConn).WriteMsgUnix(msgToWrite, nil, nil)
	}
}

func BenchmarkCgoPipeWrite(b *testing.B) {
	fd, err := makePipePair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cgoWrite(fd, msgToWrite)
	}
}

func BenchmarkGoPipeWrite(b *testing.B) {
	fd, err := makePipePair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		unix.Write(fd, msgToWrite)
	}
}

func BenchmarkGofilePipeWrite(b *testing.B) {
	fd, err := makePipePair()
	if err != nil {
		b.Fatal(err)
	}
	defer unix.Close(fd)
	f := os.NewFile(uintptr(fd), "pipe")
	defer f.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f.Write(msgToWrite)
	}
}
