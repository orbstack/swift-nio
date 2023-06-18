use std::{os::fd::RawFd};

use nix::libc::ioctl;

const LOOP_CLR_FD: u64 = 0x4C01;

pub fn clear_fd(fd: RawFd) -> std::io::Result<()> {
    let res = unsafe {
        // cmd type is different on glibc and musl???
        ioctl(fd, LOOP_CLR_FD.try_into().unwrap(), 0)
    };
    if res != 0 {
        return Err(std::io::Error::last_os_error().into());
    }
    Ok(())
}
