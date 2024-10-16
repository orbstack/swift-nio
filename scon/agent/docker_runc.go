package agent

import (
	"fmt"
	"math"
	"os"

	"golang.org/x/sys/unix"
)

// to fix CVE-2019-5736, runc 1.1.15+ makes a sealed memfd copy of itself on every start
// bind-mounting a sealed memfd copy onto /usr/bin/runc avoids this; runc detects it and uses the global memfd
func bindMountRuncMemfd() (retErr error) {
	// make memfd
	mfd, err := unix.MemfdCreate("runc", unix.MFD_CLOEXEC|unix.MFD_ALLOW_SEALING)
	if err != nil {
		return fmt.Errorf("memfd create: %w", err)
	}
	defer func() {
		// only close memfd if failed
		// leak it intentionally to preserve magic link if this succeeds
		if retErr != nil {
			unix.Close(mfd)
		}
	}()

	// open runc
	runcFile, err := os.OpenFile(runcPath, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("open runc: %w", err)
	}
	defer runcFile.Close()

	// sendfile
	for {
		// sendfile max = 2 GiB (int32)
		n, err := unix.Sendfile(mfd, int(runcFile.Fd()), nil, int(math.MaxInt32))
		if err != nil {
			return fmt.Errorf("sendfile: %w", err)
		}
		if n == 0 {
			break
		}
	}

	// seal
	_, err = unix.FcntlInt(uintptr(mfd), unix.F_ADD_SEALS, unix.F_SEAL_SEAL|unix.F_SEAL_SHRINK|unix.F_SEAL_GROW|unix.F_SEAL_WRITE)
	if err != nil {
		return fmt.Errorf("fcntl: %w", err)
	}

	// hacky trick: can't bind mount memfd because it's in kernel's internal mount ns,
	// but /proc is not, so we can use O_PATH to bind the memfd's magic link instead
	magicLinkFd, err := unix.Open(fmt.Sprintf("/proc/self/fd/%d", mfd), unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open link: %w", err)
	}
	defer unix.Close(magicLinkFd)

	err = unix.Mount(fmt.Sprintf("/proc/self/fd/%d", magicLinkFd), runcPath, "", unix.MS_BIND, "")
	if err != nil {
		return fmt.Errorf("mount: %w", err)
	}

	return nil
}
