use std::{ffi::CStr, mem::MaybeUninit, os::fd::AsRawFd};

use nix::errno::Errno;

use super::{file::fstatat, path::PATH_STACK_MAX};

pub fn with_readlinkat<F: AsRawFd, T>(
    dirfd: &F,
    path: &CStr,
    f: impl FnOnce(&[u8]) -> T,
) -> nix::Result<T> {
    let mut buf = MaybeUninit::<[u8; PATH_STACK_MAX]>::uninit();

    let ret = unsafe {
        libc::readlinkat(
            dirfd.as_raw_fd(),
            path.as_ptr(),
            buf.as_mut_ptr() as *mut _,
            PATH_STACK_MAX,
        )
    };
    let n = Errno::result(ret)? as usize;
    if n < PATH_STACK_MAX {
        // path fits in stack buffer
        let path = unsafe { std::slice::from_raw_parts(buf.as_ptr() as *const u8, ret as usize) };
        Ok(f(path))
    } else {
        // truncated
        // stat to figure out how many bytes to allocate, then do it on heap
        let mut buf = Vec::new();
        loop {
            let st = fstatat(dirfd, path, libc::AT_SYMLINK_NOFOLLOW)?;
            let expected_len = st.st_size as usize;
            // +1 so we can tell whether it was truncated or not
            buf.reserve(expected_len + 1);
            let ret = unsafe {
                libc::readlinkat(
                    dirfd.as_raw_fd(),
                    path.as_ptr(),
                    buf.as_mut_ptr() as *mut _,
                    buf.capacity(),
                )
            };
            match Errno::result(ret) {
                Ok(n) => {
                    if n as usize <= expected_len {
                        unsafe { buf.set_len(n as usize) };
                        return Ok(f(&buf));
                    }

                    // truncated due to race (symlink dest changed)
                    // try again
                }
                Err(e) => return Err(e),
            }
        }
    }
}
