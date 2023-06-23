use std::{fs::File, error::Error, os::fd::AsRawFd};

const KRPC_IOC: u8 = 0xDA;

/*
 * rvfs = rosetta vfs
 *
 * architecture:
 *   - rvfs file 0 = real rosetta (saves fd)
 *   - rvfs file 1 = new rosetta (redirects ioctl to real)
 */
mod ioctl {
    use super::*;

    nix::ioctl_write_int!(adopt_rvfs_fd0, KRPC_IOC, 3);
    nix::ioctl_write_int!(adopt_rvfs_fd1, KRPC_IOC, 4);
}

// redirect new_file ioctls to real_rosetta
pub fn adopt_rvfs_files(real_rosetta: File, new_file: File) -> Result<(), Box<dyn Error>> {
    let krpc_dev = File::open("/dev/krpc")?;
    unsafe {
        ioctl::adopt_rvfs_fd0(krpc_dev.as_raw_fd(), real_rosetta.as_raw_fd() as u64)?;
        ioctl::adopt_rvfs_fd1(krpc_dev.as_raw_fd(), new_file.as_raw_fd() as u64)?;
    }

    Ok(())
}
