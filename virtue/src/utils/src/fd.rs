use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};

use nix::errno::Errno;

pub fn dup_fd<F: AsRawFd>(f: F) -> nix::Result<OwnedFd> {
    // start at 3 to prevent racing/overwriting stdin/stdout/stderr
    let ret = unsafe { libc::fcntl(f.as_raw_fd(), libc::F_DUPFD_CLOEXEC, 3) };
    let fd = Errno::result(ret)?;

    // SAFETY: the fd is valid because dup succeeded
    Ok(unsafe { OwnedFd::from_raw_fd(fd) })
}
