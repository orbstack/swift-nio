use std::os::fd::RawFd;

use libc::c_int;
use nix::{
    errno::Errno,
    fcntl::{
        fcntl,
        FcntlArg::{self, F_GETFD},
        FdFlag,
    },
    mount::MsFlags,
};

pub mod asyncfile;
pub mod flock;
pub mod model;
pub mod newmount;
pub mod paths;
pub mod rpc;
pub mod termios;

fn _err<T: IsMinusOne>(ret: T) -> nix::Result<T> {
    if ret.is_minus_one() {
        Err(Errno::last())
    } else {
        Ok(ret)
    }
}

pub fn err<T: IsMinusOne + Copy>(ret: T) -> nix::Result<T> {
    loop {
        match _err(ret) {
            Err(Errno::EINTR) => {}
            other => return other,
        }
    }
}

pub trait IsMinusOne {
    fn is_minus_one(&self) -> bool;
}

impl IsMinusOne for i64 {
    fn is_minus_one(&self) -> bool {
        *self == -1
    }
}

impl IsMinusOne for i32 {
    fn is_minus_one(&self) -> bool {
        *self == -1
    }
}

impl IsMinusOne for isize {
    fn is_minus_one(&self) -> bool {
        *self == -1
    }
}

pub fn set_cloexec(fd: RawFd) -> Result<c_int, Errno> {
    fcntl(
        fd,
        FcntlArg::F_SETFD(FdFlag::from_bits_retain(fcntl(fd, F_GETFD)?) | FdFlag::FD_CLOEXEC),
    )
}

pub fn unset_cloexec(fd: RawFd) -> Result<c_int, Errno> {
    fcntl(
        fd,
        FcntlArg::F_SETFD(FdFlag::from_bits_retain(fcntl(fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC),
    )
}

pub fn mount_common(
    source: &str,
    dest: &str,
    fstype: Option<&str>,
    flags: MsFlags,
    data: Option<&str>,
) -> anyhow::Result<()> {
    nix::mount::mount(Some(source), dest, fstype, flags, data)?;
    Ok(())
}

pub fn bind_mount(source: &str, dest: &str, flags: Option<MsFlags>) -> anyhow::Result<()> {
    mount_common(
        source,
        dest,
        None,
        flags.unwrap_or(MsFlags::empty()) | MsFlags::MS_BIND,
        None,
    )
}

pub fn bind_mount_ro(source: &str, dest: &str) -> anyhow::Result<()> {
    bind_mount(source, dest, None)?;
    // then we have to remount as ro with MS_REMOUNT | MS_BIND | MS_RDONLY
    bind_mount(dest, dest, Some(MsFlags::MS_REMOUNT | MsFlags::MS_RDONLY))?;
    Ok(())
}
