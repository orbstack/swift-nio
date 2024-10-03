use std::{ffi::CStr, mem::MaybeUninit, os::fd::AsRawFd};

use nix::errno::Errno;

use super::path::PATH_STACK_MAX;

const XATTR_VALUE_STACK_MAX: usize = 512;

fn for_each_cstr(data: &[u8], mut f: impl FnMut(&CStr) -> nix::Result<()>) -> nix::Result<()> {
    let mut slice = data;
    while let Ok(cstr) = CStr::from_bytes_until_nul(slice) {
        f(cstr)?;
        slice = &slice[cstr.count_bytes() + 1..];
    }
    Ok(())
}

pub fn for_each_flistxattr<F: AsRawFd>(fd: &F, f: impl FnMut(&CStr) -> nix::Result<()>) -> nix::Result<()> {
    let mut buf = MaybeUninit::<[u8; XATTR_VALUE_STACK_MAX]>::uninit();

    let ret = unsafe { libc::flistxattr(fd.as_raw_fd(), buf.as_mut_ptr() as *mut _, XATTR_VALUE_STACK_MAX) };
    match Errno::result(ret) {
        Ok(n) => {
            if n > 0 {
                let data = unsafe { std::slice::from_raw_parts(buf.as_ptr() as *const u8, n as usize) };
                for_each_cstr(data, f)?;
            }
            Ok(())
        }

        Err(Errno::ERANGE) => {
            let mut buf = Vec::new();

            // loop in case of race (xattr added between calls)
            loop {
                // get requested size
                let ret = unsafe { libc::flistxattr(fd.as_raw_fd(), std::ptr::null_mut(), 0) };
                let expected_len = Errno::result(ret)? as usize;
                buf.reserve_exact(expected_len);

                // get xattr list again
                let ret = unsafe { libc::flistxattr(fd.as_raw_fd(), buf.as_mut_ptr() as *mut _, buf.capacity()) };
                match Errno::result(ret) {
                    Ok(n) => {
                        if n > 0 {
                            unsafe { buf.set_len(n as usize) };
                            for_each_cstr(&buf, f)?;
                        }
                        return Ok(());
                    }

                    // raced, try again
                    Err(Errno::ERANGE) => continue,

                    // other error
                    Err(e) => return Err(e),
                }
            }
        }

        Err(e) => Err(e),
    }
}

pub fn with_fgetxattr<T, F: AsRawFd>(fd: &F, name: &CStr, f: impl FnOnce(&[u8]) -> T) -> nix::Result<T> {
    let mut buf = MaybeUninit::<[u8; PATH_STACK_MAX]>::uninit();

    let ret = unsafe { libc::fgetxattr(fd.as_raw_fd(), name.as_ptr(), buf.as_mut_ptr() as *mut _, PATH_STACK_MAX) };
    match Errno::result(ret) {
        Ok(n) => {
            let data = unsafe { std::slice::from_raw_parts(buf.as_ptr() as *const u8, n as usize) };
            Ok(f(data))
        }
        Err(Errno::ERANGE) => {
            let mut buf = Vec::new();
            loop {
                // get requested size
                let ret = unsafe { libc::fgetxattr(fd.as_raw_fd(), name.as_ptr(), std::ptr::null_mut(), 0) };
                let expected_len = Errno::result(ret)? as usize;
                buf.reserve_exact(expected_len);

                // get xattr again
                let ret = unsafe { libc::fgetxattr(fd.as_raw_fd(), name.as_ptr(), buf.as_mut_ptr() as *mut _, buf.capacity()) };
                match Errno::result(ret) {
                    Ok(n) => {
                        unsafe { buf.set_len(n as usize) };
                        return Ok(f(&buf));
                    }
                    Err(Errno::ERANGE) => continue,
                    Err(e) => return Err(e),
                }
            }
        }
        Err(e) => Err(e),
    }
}
