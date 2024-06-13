use std::io;

use nix::errno::Errno;

extern "C" {
    fn fsgetpath(
        restrict_buf: *mut libc::c_char,
        buflen: libc::size_t,
        fsid: *const libc::fsid_t,
        obj_id: u64,
    ) -> libc::c_int;
}

fn _fsgetpath(
    restrict_buf: *mut libc::c_char,
    buflen: libc::size_t,
    fsid: *const libc::fsid_t,
    obj_id: u64,
) -> nix::Result<usize> {
    let ret = unsafe { fsgetpath(restrict_buf, buflen, fsid, obj_id) };
    if ret == -1 {
        return Err(nix::Error::last());
    }
    Ok(ret as usize)
}

// faster than access(format!("/.vol/$DEV/$INO"), F_OK)
pub fn fsgetpath_exists(fsid: libc::fsid_t, ino: u64) -> nix::Result<bool> {
    match _fsgetpath(std::ptr::null_mut(), 1, &fsid as *const libc::fsid_t, ino) {
        // should never succeed, but if it somehow does, the path definitely exists
        Ok(_) => Ok(true),

        // success: kernel tried to copy path
        // if kernel checks pointer first, error will be EFAULT
        Err(Errno::EFAULT) => Ok(true),
        // if kernel checks buflen first, error will be ENOSPC
        Err(Errno::ENOSPC) => Ok(true),

        // ENOENT: doesn't exist
        Err(Errno::ENOENT) => Ok(false),

        // failed: any other error
        Err(e) => Err(e),
    }
}
