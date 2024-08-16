use nix::libc::size_t;
use std::os::fd::AsRawFd;

mod ioctl {
    use super::*;

    nix::ioctl_read!(blkgetsize64, 0x12, 114, size_t);
}

pub fn getsize64(path: &str) -> anyhow::Result<u64> {
    let file = std::fs::File::open(path)?;
    let fd = file.as_raw_fd();
    let mut size: size_t = 0;
    unsafe {
        ioctl::blkgetsize64(fd, &mut size)?;
    }
    Ok(size as u64)
}
