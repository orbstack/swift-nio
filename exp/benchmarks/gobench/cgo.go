package main

/*
#include <stdio.h>
#include <unistd.h>
#include <sys/uio.h>
#include <sys/socket.h>

int writev_one(int fd, struct iovec iov) {
	return writev(fd, &iov, 1);
}

int sendmsg_one(int fd, struct iovec iov) {
	struct msghdr msg = {0};
	msg.msg_iov = &iov;
	msg.msg_iovlen = 1;
	return sendmsg(fd, &msg, 0);
}
*/
import "C"
import "unsafe"

type cgoIovec = C.struct_iovec
type cgoUlong = C.ulong
type cgoMsghdr = C.struct_msghdr

func cgoGetpid() {
	C.getpid()
}

func cgoWrite(fd int, buf []byte) int {
	ptr := &buf[0]
	return int(C.write(C.int(fd), unsafe.Pointer(ptr), C.size_t(len(buf))))
}

func cgoWritev(fd int, iov1 C.struct_iovec) int {
	return int(C.writev_one(C.int(fd), iov1))
}

func cgoSendmsg(fd int, msg C.struct_iovec) int {
	return int(C.sendmsg_one(C.int(fd), msg))
}
