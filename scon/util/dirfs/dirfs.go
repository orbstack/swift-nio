package dirfs

import (
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/orbstack/macvirt/scon/util"
	"golang.org/x/sys/unix"
)

type FS struct {
	dfd    *os.File
	dfdRaw syscall.RawConn
}

func NewFromPath(root string) (*FS, error) {
	dfd, err := unix.Open(root, unix.O_DIRECTORY|unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}

	file := os.NewFile(uintptr(dfd), root)
	return NewFromDirfd(file)
}

func NewFromDirfd(dfd *os.File) (*FS, error) {
	dfdRaw, err := dfd.SyscallConn()
	if err != nil {
		return nil, err
	}

	return &FS{
		dfd:    dfd,
		dfdRaw: dfdRaw,
	}, nil
}

var Default = sync.OnceValue(func() *FS {
	fs, err := NewFromPath("/")
	if err != nil {
		panic(err)
	}

	return fs
})

func (fs *FS) Close() error {
	return fs.dfd.Close()
}

func (fs *FS) Dirfd() *os.File {
	return fs.dfd
}

func (fs *FS) OpenFd(name string, flag int, perm os.FileMode) (int, error) {
	for {
		fd, err := util.UseRawConn1(fs.dfdRaw, func(fd int) (int, error) {
			return unix.Openat(fd, name, flag|unix.O_CLOEXEC, uint32(perm))
		})
		if err != nil {
			// need to check for EINTR - Go issues 11180, 39237
			if err == unix.EINTR {
				continue
			} else {
				return 0, err
			}
		}

		return fd, nil
	}
}

func (fs *FS) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := fs.OpenFd(name, flag, perm)
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(fd), name), nil
}

func (fs *FS) Open(name string) (*os.File, error) {
	return fs.OpenFile(name, os.O_RDONLY, 0)
}

func (fs *FS) Create(name string) (*os.File, error) {
	return fs.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

func (fs *FS) ReadFile(name string) ([]byte, error) {
	f, err := fs.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return io.ReadAll(f)
}

func (fs *FS) WriteFile(name string, data []byte, perm os.FileMode) error {
	f, err := fs.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(data)
	return err
}

func (fs *FS) ReadDir(name string) ([]os.DirEntry, error) {
	f, err := fs.OpenFile(name, unix.O_DIRECTORY|unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return f.ReadDir(0)
}

func (fs *FS) Stat(name string) (os.FileInfo, error) {
	f, err := fs.OpenFile(name, unix.O_PATH, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return f.Stat()
}

func (fs *FS) ResolvePath(name string) (string, error) {
	file, err := fs.OpenFile(name, unix.O_PATH, 0)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// from magic link
	return os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
}
