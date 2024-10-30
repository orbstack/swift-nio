use std::os::fd::{AsFd, AsRawFd, BorrowedFd, FromRawFd, OwnedFd, RawFd};

use nix::{
    libc::{syscall, SYS_pidfd_open, PIDFD_NONBLOCK},
    poll::{poll, PollFd, PollFlags},
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

    #[allow(dead_code)]
    pub fn wait(&self) -> std::io::Result<()> {
        let pollfd = PollFd::new(&self.0, PollFlags::POLLIN);
        poll(&mut [pollfd], -1)?;
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
