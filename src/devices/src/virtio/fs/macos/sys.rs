use nix::errno::Errno;

mod raw {
    extern "C" {
        pub fn fsgetpath(
            restrict_buf: *mut libc::c_char,
            buflen: libc::size_t,
            fsid: *const libc::fsid_t,
            obj_id: u64,
        ) -> libc::c_int;
    }
}

fn fsgetpath_null(buflen: libc::size_t, fsid: &libc::fsid_t, obj_id: u64) -> nix::Result<usize> {
    let ret = unsafe { raw::fsgetpath(std::ptr::null_mut(), buflen, fsid, obj_id) };
    Errno::result(ret).map(|r| r as usize)
}

// faster than access(format!("/.vol/$DEV/$INO"), F_OK)
pub fn fsgetpath_exists(fsid: libc::fsid_t, ino: u64) -> nix::Result<bool> {
    match fsgetpath_null(1, &fsid, ino) {
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
