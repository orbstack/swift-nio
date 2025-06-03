use std::os::fd::{AsFd, AsRawFd, BorrowedFd, FromRawFd, OwnedFd, RawFd};

use nix::{
    errno::Errno,
    libc::{siginfo_t, syscall, SYS_pidfd_open, SYS_pidfd_send_signal, PIDFD_NONBLOCK},
    poll::{poll, PollFd, PollFlags, PollTimeout},
    sys::signal::Signal,
};

pub struct PidFd(OwnedFd);

impl PidFd {
    pub fn open(pid: i32) -> std::io::Result<Self> {
        let fd = unsafe { syscall(SYS_pidfd_open, pid, PIDFD_NONBLOCK) };
        if fd == -1 {
            return Err(std::io::Error::last_os_error());
        }
        let fd = unsafe { OwnedFd::from_raw_fd(fd as _) };
        Ok(Self(fd))
    }

    pub fn kill(&self, signal: Signal) -> nix::Result<()> {
        let res = unsafe {
            syscall(
                SYS_pidfd_send_signal,
                self.as_raw_fd(),
                signal,
                std::ptr::null::<*const siginfo_t>(),
                0,
            )
        };
        if res < 0 {
            return Err(Errno::last());
        }

        Ok(())
    }

    #[allow(dead_code)]
    pub fn wait(&self) -> std::io::Result<()> {
        let pollfd = PollFd::new(self.0.as_fd(), PollFlags::POLLIN);
        poll(&mut [pollfd], PollTimeout::NONE)?;
        Ok(())
    }
}

impl AsRawFd for PidFd {
    fn as_raw_fd(&self) -> RawFd {
        self.0.as_raw_fd()
    }
}

impl AsFd for PidFd {
    fn as_fd(&self) -> BorrowedFd<'_> {
        self.0.as_fd()
    }
}
