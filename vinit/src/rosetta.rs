use std::{fs::{File, self}, error::Error, os::fd::AsRawFd};
use qbsdiff::Bspatch;

const KRPC_IOC: u8 = 0xDA;

const ROSETTA_FINGERPRINT_SALT: &[u8] = b"orbrosettafp";
const ROSETTA_BUFFER: usize = 524288;

#[derive(thiserror::Error, Debug)]
pub enum RosettaError {
    #[error("unknown build: {}", .0)]
    UnknownBuild(String),
    #[error("apply failed: {}", .0)]
    ApplyFailed(#[from] Box<dyn Error>),
}

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

fn hash_with_salt(salt: &[u8], data: &[u8]) -> Result<[u8; 32], Box<dyn Error>> {
    let mut hasher = blake3::Hasher::new();
    hasher.update(salt);
    hasher.update(data);

    Ok(hasher.finalize().into())
}

pub fn find_and_apply_patch(source_data: &[u8], dest_path: &str) -> Result<(), RosettaError> {
    // hash with salt to get fingerprint
    let fingerprint = hash_with_salt(ROSETTA_FINGERPRINT_SALT, source_data)
        .map_err(|e| RosettaError::ApplyFailed(e.into()))?;

    // find and read patch file
    let patch = fs::read(format!("/opt/orb/rvdelta/{}", hex::encode(fingerprint)))
        .map_err(|_| RosettaError::UnknownBuild(hex::encode(fingerprint)))?;

    // empty file = no patch needed
    if patch.is_empty() {
        return Ok(());
    }

    // apply patch
    let mut target = File::create(dest_path)
        .map_err(|e| RosettaError::ApplyFailed(e.into()))?;
    Bspatch::new(&patch)
        .map_err(|e| RosettaError::ApplyFailed(e.into()))?
        .buffer_size(ROSETTA_BUFFER)
        .delta_min(ROSETTA_BUFFER)
        .apply(&source_data, &mut target)
        .map_err(|e| RosettaError::ApplyFailed(e.into()))?;

    Ok(())
}
