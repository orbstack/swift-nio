use std::{ffi::CStr, mem::MaybeUninit, os::fd::AsRawFd};

use nix::{errno::Errno, NixPath};

const XATTR_DATA_STACK_MAX: usize = super::path::PATH_STACK_MAX;

struct CStrIter<'a> {
    data: &'a [u8],
}

impl<'a> CStrIter<'a> {
    fn new(data: &'a [u8]) -> Self {
        Self { data }
    }
}

impl<'a> Iterator for CStrIter<'a> {
    type Item = &'a CStr;

    fn next(&mut self) -> Option<Self::Item> {
        if self.data.is_empty() {
            return None;
        }

        let cstr = CStr::from_bytes_until_nul(self.data).unwrap();
        self.data = &self.data[cstr.count_bytes() + 1..];
        Some(cstr)
    }
}

fn read_xattr_data(
    read_fn: impl Fn(*mut u8, usize) -> nix::Result<isize>,
    mut f: impl FnMut(&[u8]) -> nix::Result<()>,
) -> nix::Result<()> {
    let mut buf = MaybeUninit::<[u8; XATTR_DATA_STACK_MAX]>::uninit();

    // happy path: fits on stack
    match read_fn(buf.as_mut_ptr() as *mut _, XATTR_DATA_STACK_MAX) {
        Ok(n) => {
            if n > 0 {
                let data =
                    unsafe { std::slice::from_raw_parts(buf.as_ptr() as *const u8, n as usize) };
                f(data)?;
            }

            return Ok(());
        }

        // fallthrough to slow path
        Err(Errno::ERANGE) => {}

        Err(e) => return Err(e),
    }

    // slow path: doesn't fit on stack
    let mut buf = Vec::new();

    // loop in case of race (xattr added between calls)
    loop {
        // get requested size
        let expected_len = read_fn(std::ptr::null_mut(), 0)? as usize;
        buf.reserve_exact(expected_len);

        // get xattr list again
        match read_fn(buf.as_mut_ptr() as *mut _, buf.capacity()) {
            Ok(n) => {
                if n > 0 {
                    unsafe { buf.set_len(n as usize) };
                    f(&buf)?;
                }

                break;
            }

            // raced, try again
            Err(Errno::ERANGE) => continue,

            // other error
            Err(e) => return Err(e),
        }
    }

    Ok(())
}

pub fn for_each_flistxattr<F: AsRawFd>(
    fd: &F,
    mut f: impl FnMut(&CStr) -> nix::Result<()>,
) -> nix::Result<()> {
    read_xattr_data(
        |buf, size| {
            let ret = unsafe { libc::flistxattr(fd.as_raw_fd(), buf, size) };
            Errno::result(ret)
        },
        |data| {
            for cstr in CStrIter::new(data) {
                f(cstr)?;
            }

            Ok(())
        },
    )
}

pub fn for_each_llistxattr<P: NixPath + ?Sized>(
    path: &P,
    mut f: impl FnMut(&CStr) -> nix::Result<()>,
) -> nix::Result<()> {
    path.with_nix_path(|path| {
        read_xattr_data(
            |buf, size| {
                let ret = unsafe { libc::llistxattr(path.as_ptr(), buf, size) };
                Errno::result(ret)
            },
            |data| {
                for cstr in CStrIter::new(data) {
                    f(cstr)?;
                }

                Ok(())
            },
        )
    })?
}

pub fn with_fgetxattr<F: AsRawFd>(
    fd: &F,
    name: &CStr,
    mut f: impl FnMut(&[u8]) -> nix::Result<()>,
) -> nix::Result<()> {
    read_xattr_data(
        |buf, size| {
            let ret =
                unsafe { libc::fgetxattr(fd.as_raw_fd(), name.as_ptr(), buf as *mut _, size) };
            Errno::result(ret)
        },
        |data| {
            f(data)?;
            Ok(())
        },
    )
}

pub fn with_lgetxattr<P: NixPath + ?Sized>(
    path: &P,
    name: &CStr,
    mut f: impl FnMut(&[u8]) -> nix::Result<()>,
) -> nix::Result<()> {
    path.with_nix_path(|path| {
        read_xattr_data(
            |buf, size| {
                let ret =
                    unsafe { libc::lgetxattr(path.as_ptr(), name.as_ptr(), buf as *mut _, size) };
                Errno::result(ret)
            },
            |data| {
                f(data)?;
                Ok(())
            },
        )
    })?
}

pub fn fsetxattr<F: AsRawFd>(fd: &F, name: &CStr, value: &[u8], flags: i32) -> nix::Result<()> {
    let ret = unsafe {
        libc::fsetxattr(
            fd.as_raw_fd(),
            name.as_ptr(),
            value.as_ptr() as *const _,
            value.len() as libc::size_t,
            flags,
        )
    };
    Errno::result(ret)?;
    Ok(())
}

pub fn lsetxattr<P: NixPath + ?Sized>(
    path: &P,
    name: &CStr,
    value: &[u8],
    flags: i32,
) -> nix::Result<()> {
    path.with_nix_path(|path| {
        let ret = unsafe {
            libc::lsetxattr(
                path.as_ptr(),
                name.as_ptr(),
                value.as_ptr() as *const _,
                value.len() as libc::size_t,
                flags,
            )
        };
        Errno::result(ret)?;
        Ok(())
    })?
}
