use qbsdiff::Bspatch;
use std::{
    error::Error,
    fs::{self, File},
    os::fd::AsRawFd,
    process::Command,
};

const KRPC_IOC: u8 = 0xDA;

const ROSETTA_FINGERPRINT_SALT: &[u8] = b"orb\x00rosetta\x00fp";
const ROSETTA_BUFFER: usize = 524288;

// docs in rvfs-wrapper.c
const NODEJS_PRELOAD_SCRIPT: &[u8] = b"const p=require('process');p.execArgv=p.execArgv.slice(3)";
const PROCP_SIZE: usize = 256;

pub const RSTUB_FLAG_TSO_WORKAROUND: u32 = 1 << 0;

#[derive(thiserror::Error, Debug)]
pub enum RosettaError {
    #[error("unknown build: {}", .0)]
    UnknownBuild(String),
    #[error("other error: {}", .0)]
    Other(#[from] Box<dyn Error>),
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
    nix::ioctl_write_buf!(set_procp, KRPC_IOC, 5, u8);
}

// redirect new_file ioctls to real_rosetta
pub fn adopt_rvfs_files(real_rosetta: File, new_file: File) -> Result<(), Box<dyn Error>> {
    let krpc_dev = File::open("/dev/krpc")?;
    unsafe {
        ioctl::adopt_rvfs_fd0(krpc_dev.as_raw_fd(), real_rosetta.as_raw_fd() as u64)?;
        ioctl::adopt_rvfs_fd1(krpc_dev.as_raw_fd(), new_file.as_raw_fd() as u64)?;
    }

    // while we're at it, also set the procp data to the nodejs preload script for no-opt execArgv
    // this is a null-terminated buffer of up to 256 bytes
    let mut procp = [0u8; PROCP_SIZE];
    procp[..NODEJS_PRELOAD_SCRIPT.len()].copy_from_slice(NODEJS_PRELOAD_SCRIPT);
    unsafe {
        ioctl::set_procp(krpc_dev.as_raw_fd(), &procp)?;
    }

    Ok(())
}

fn start_hash(salt: &[u8], data: &[u8]) -> blake3::Hasher {
    let mut hasher = blake3::Hasher::new();
    hasher.update(salt);
    hasher.update(data);
    hasher
}

fn apply_xof(hasher: &mut blake3::Hasher, patch: &mut [u8]) {
    let mut xof = hasher.finalize_xof();
    let mut buf = [0u8; ROSETTA_BUFFER];
    let mut offset = 0;

    // skip hash (first block)
    xof.set_position(64);

    while offset < patch.len() {
        let len = std::cmp::min(ROSETTA_BUFFER, patch.len() - offset);
        xof.fill(&mut buf);
        // XOR
        for i in 0..len {
            patch[offset + i] ^= buf[i];
        }
        offset += len;
    }
}

pub fn find_and_apply_patch(source_data: &[u8], dest_path: &str) -> Result<(), RosettaError> {
    // hash with salt to get fingerprint
    let mut hasher = start_hash(ROSETTA_FINGERPRINT_SALT, source_data);
    let fingerprint: [u8; 32] = hasher.finalize().into();

    // find and read patch file
    let mut patch = fs::read(format!("/opt/orb/delta/{}", hex::encode(fingerprint)))
        .map_err(|_| RosettaError::UnknownBuild(hex::encode(fingerprint)))?;

    // empty file = no patch needed
    if patch.is_empty() {
        return Ok(());
    }

    // decrypt patch (~1 ms in release, 20 ms debug)
    apply_xof(&mut hasher, &mut patch);

    // apply patch
    let mut target = File::create(dest_path).map_err(|e| RosettaError::Other(e.into()))?;
    Bspatch::new(&patch)
        .map_err(|e| RosettaError::Other(e.into()))?
        .buffer_size(ROSETTA_BUFFER)
        .delta_min(ROSETTA_BUFFER)
        .apply(source_data, &mut target)
        .map_err(|e| RosettaError::Other(e.into()))?;

    Ok(())
}

pub fn get_version(rosetta_path: &str) -> Result<String, Box<dyn Error>> {
    // run it to get the version
    let output = Command::new(rosetta_path).output()?;

    // get last line
    let output = String::from_utf8(output.stderr)?;
    let last_line = output.trim().lines().last().ok_or("no output")?;

    // parse version: last field
    let version = last_line.split_whitespace().last().ok_or("no version")?;

    Ok(version.into())
}
