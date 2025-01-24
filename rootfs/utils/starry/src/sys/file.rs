use std::{
    ffi::CStr,
    mem::MaybeUninit,
    os::fd::{AsRawFd, RawFd},
};

use nix::errno::Errno;

pub struct AtFdcwd {}

const AT_FDCWD_INSTANCE: AtFdcwd = AtFdcwd {};
pub static AT_FDCWD: &AtFdcwd = &AT_FDCWD_INSTANCE;

impl AsRawFd for AtFdcwd {
    fn as_raw_fd(&self) -> RawFd {
        libc::AT_FDCWD
    }
}

pub fn fstat<F: AsRawFd>(fd: &F) -> nix::Result<libc::stat> {
    let fd = fd.as_raw_fd();
    let mut st = MaybeUninit::uninit();
    let ret = unsafe { libc::fstat(fd, st.as_mut_ptr()) };
    Errno::result(ret).map(|_| unsafe { st.assume_init() })
}

pub fn fstatat<F: AsRawFd>(dirfd: &F, path: &CStr, flags: i32) -> nix::Result<libc::stat> {
    let dirfd = dirfd.as_raw_fd();
    let mut st = MaybeUninit::uninit();
    let ret = unsafe { libc::fstatat(dirfd, path.as_ptr(), st.as_mut_ptr(), flags) };
    Errno::result(ret).map(|_| unsafe { st.assume_init() })
}

pub fn unlinkat<F: AsRawFd>(dirfd: &F, path: &CStr, flags: i32) -> nix::Result<()> {
    let dirfd = dirfd.as_raw_fd();
    let ret = unsafe { libc::unlinkat(dirfd, path.as_ptr(), flags) };
    Errno::result(ret).map(drop)
}

pub fn fchownat<F: AsRawFd>(
    dirfd: &F,
    path: &CStr,
    uid: u32,
    gid: u32,
    flags: i32,
) -> nix::Result<()> {
    let dirfd = dirfd.as_raw_fd();
    let ret = unsafe { libc::fchownat(dirfd, path.as_ptr(), uid, gid, flags) };
    Errno::result(ret).map(drop)
}
