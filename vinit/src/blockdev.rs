use std::{error::Error, os::fd::AsRawFd};
use nix::libc::size_t;

mod ioctl {
    use super::*;

    nix::ioctl_read!(blkgetsize64, 0x12, 114, size_t);
}

pub fn getsize64(path: &str) -> Result<u64, Box<dyn Error>> {
    let file = std::fs::File::open(path)?;
    let fd = file.as_raw_fd();
    let mut size: size_t = 0;
    unsafe {
        ioctl::blkgetsize64(fd, &mut size)?;
    }
    Ok(size as u64)
}
