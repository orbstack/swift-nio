use std::os::fd::RawFd;

use libc::c_int;
use nix::{
    errno::Errno,
    fcntl::{
        fcntl,
        FcntlArg::{self, F_GETFD},
        FdFlag,
    },
};

pub mod asyncfile;
pub mod flock;
pub mod newmount;
pub mod paths;

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
        FcntlArg::F_SETFD(FdFlag::from_bits_truncate(fcntl(fd, F_GETFD)?) & !FdFlag::FD_CLOEXEC),
    )
}
