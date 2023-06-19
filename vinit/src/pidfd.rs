use std::os::fd::{OwnedFd, FromRawFd, AsRawFd, RawFd};

use nix::{libc::{syscall, SYS_pidfd_open, PIDFD_NONBLOCK, SYS_pidfd_send_signal, siginfo_t}, sys::signal::Signal};
use tokio::io::unix::AsyncFd;

pub struct PidFd(pub AsyncFd<OwnedFd>);

impl PidFd {
    pub fn open(pid: i32) -> std::io::Result<Self> {
        let fd = unsafe { syscall(SYS_pidfd_open, pid, PIDFD_NONBLOCK) };
        if fd < 0 {
            return Err(std::io::Error::last_os_error());
        }
        let fd = unsafe { OwnedFd::from_raw_fd(fd as _) };
        let fd = AsyncFd::new(fd)?;
        Ok(Self(fd))
    }

    pub fn send_signal(&self, signal: Signal) -> std::io::Result<()> {
        let res = unsafe { syscall(SYS_pidfd_send_signal, self.as_raw_fd(), signal as i32, std::ptr::null::<*const siginfo_t>(), 0) };
        if res < 0 {
            return Err(std::io::Error::last_os_error());
        }

        Ok(())
    }
}

impl AsRawFd for PidFd {
    fn as_raw_fd(&self) -> RawFd {
        self.0.as_raw_fd()
    }
}
