use std::os::fd::{OwnedFd, FromRawFd, AsRawFd, RawFd};

use nix::{libc::{syscall, SYS_pidfd_open, PIDFD_NONBLOCK, SYS_pidfd_send_signal, siginfo_t}, sys::signal::Signal};
use tokio::io::unix::{AsyncFd, AsyncFdReadyGuard};

pub struct PidFd(AsyncFd<OwnedFd>);

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

    fn send_signal(&self, signal: Option<Signal>) -> nix::Result<()> {
        let signal = signal.map_or(0, |s| s as i32);
        let res = unsafe { syscall(SYS_pidfd_send_signal, self.as_raw_fd(), signal, std::ptr::null::<*const siginfo_t>(), 0) };
        if res < 0 {
            return Err(nix::Error::last());
        }

        Ok(())
    }

    pub fn kill(&self, signal: Signal) -> nix::Result<()> {
        self.send_signal(Some(signal))
    }

    pub fn is_alive(&self) -> std::io::Result<bool> {
        match self.send_signal(None) {
            // success = process is alive
            Ok(_) => Ok(true),
            Err(e) => {
                if e == nix::errno::Errno::ESRCH {
                    // ESRCH = process is dead
                    Ok(false)
                } else {
                    // shouldn't get other errors
                    Err(std::io::Error::from(e))
                }
            },
        }
    }

    pub async fn wait(&self) -> tokio::io::Result<()> {
        loop {
            let mut guard = self.0.readable().await?;
            // test process by sending signal 0
            match guard.try_io(|_| self.is_alive()) {
                Ok(Ok(false)) => return Ok(()),
                Ok(Ok(true)) => continue,
                Ok(Err(e)) => return Err(e),
                Err(_would_block) => continue,
            }
        }
    }
}

impl AsRawFd for PidFd {
    fn as_raw_fd(&self) -> RawFd {
        self.0.as_raw_fd()
    }
}
