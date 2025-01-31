/*
 * starry rm
 * similar to `rm -rf`
 *
 * features:
 * - handles inode flags that prevent deletion (immutable, append-only)
 * - safe against symlink races (everything is dirfd/O_NOFOLLOW)
 *
 * assumptions:
 * - source is NOT modified concurrently. if this is violated, the command may fail, but there is no security risk (in the case of symlink races). specifically, deletion races may cause the entire command to fail.
 * - should be run as root in order to remove inode flags
 */

use std::{
    ffi::{CStr, CString},
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    path::Path,
};

use crate::recurse::Recurser;
use crate::sys::{
    file::{fstatat, unlinkat},
    getdents::{DirEntry, FileType},
    inode_flags::InodeFlags,
};
use nix::{
    errno::Errno,
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};

fn clear_flags(fd: &OwnedFd) -> nix::Result<()> {
    let mut flags = InodeFlags::from_file(fd)?;
    if flags.intersects(InodeFlags::IMMUTABLE | InodeFlags::APPEND) {
        flags.remove(InodeFlags::IMMUTABLE | InodeFlags::APPEND);
        flags.apply(fd)?;
    }
    Ok(())
}

fn unlinkat_and_clear_flags(dirfd: &OwnedFd, path: &CStr, unlink_flags: i32) -> nix::Result<()> {
    // common case: try plain unlink first
    match unlinkat(dirfd, path, unlink_flags) {
        Ok(_) => Ok(()),
        Err(Errno::EPERM) => {
            // on EPERM (not EACCES), try to clear flags and remove again
            clear_flags(dirfd)?;

            // both parent and child flags will prevent deletion, so clear from child too
            // O_PATH doesn't work and returns EBADF :(
            // before opening, stat to make sure that it's a regular file.
            // if it's a socket/symlink: this will fail the whole operation even if parent dir's flag was cleared
            // if it's a char/block device: this could hang
            // this is a slowpath, so it's fine to stat
            let st = fstatat(dirfd, path, libc::AT_SYMLINK_NOFOLLOW)?;
            if st.st_mode & libc::S_IFMT == libc::S_IFREG
                || st.st_mode & libc::S_IFMT == libc::S_IFDIR
            {
                let fd = unsafe {
                    OwnedFd::from_raw_fd(openat(
                        Some(dirfd.as_raw_fd()),
                        path,
                        OFlag::O_RDONLY
                            | OFlag::O_CLOEXEC
                            // limit damage in case of race
                            | OFlag::O_NONBLOCK
                            | OFlag::O_NOCTTY
                            | OFlag::O_NOFOLLOW,
                        Mode::empty(),
                    )?)
                };

                clear_flags(&fd)?;
            }

            // retry
            // no need to skip retry if no flags were cleared: this is already a slowpath and will fail the whole program
            unlinkat(dirfd, path, unlink_flags)
        }
        Err(e) => Err(e),
    }
}

struct OwnedRmContext {
    recurser: Recurser,
}

impl OwnedRmContext {
    fn new() -> anyhow::Result<Self> {
        Ok(Self {
            recurser: Recurser::new()?,
        })
    }
}

struct RmContext<'a> {
    recurser: &'a Recurser,
}

impl<'a> RmContext<'a> {
    fn new(owned: &'a OwnedRmContext) -> Self {
        Self {
            recurser: &owned.recurser,
        }
    }

    fn do_one_entry(&mut self, dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
        // assume file/symlink/fifo/chr/blk/socket, unless we know it's definitely a dir
        // this is always correct on filesystems that populate d_type
        // with DT_UNKNOWN, it's still faster because we just replace the fstatat() call with unlinkat(), and avoid fstatat() in the common case (there are usually more files than dirs)
        if entry.file_type != FileType::Directory {
            match unlinkat_and_clear_flags(dirfd, entry.name, 0) {
                Ok(_) => return Ok(()),
                // guessed wrong: it's a dir
                Err(Errno::EISDIR) => (),
                Err(e) => return Err(e.into()),
            }
        }

        // assumption is wrong (or FS provides d_type=DT_DIR): it's a dir
        // recursively unlink children, then unlink dir
        let child_dirfd = unsafe {
            OwnedFd::from_raw_fd(openat(
                Some(dirfd.as_raw_fd()),
                entry.name,
                OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
                Mode::empty(),
            )?)
        };
        self.walk_dir(&child_dirfd)?;
        // close first so that the unlink below can delete structures immediately instead of being deferred
        drop(child_dirfd);

        unlinkat_and_clear_flags(dirfd, entry.name, libc::AT_REMOVEDIR)?;
        Ok(())
    }

    fn walk_dir(&mut self, dirfd: &OwnedFd) -> anyhow::Result<()> {
        self.recurser
            .walk_dir(dirfd, None, |entry| self.do_one_entry(dirfd, entry))?;
        Ok(())
    }
}

pub fn main(src_dir: &str) -> anyhow::Result<()> {
    // open root dir
    let root_dir = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&src_dir),
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };

    // walk dirs
    let owned_ctx = OwnedRmContext::new()?;
    let mut ctx = RmContext::new(&owned_ctx);
    let src_dir_cstr = CString::new(src_dir.as_bytes())?;
    ctx.recurser
        .walk_dir_root(&root_dir, &src_dir_cstr, None, |entry| {
            ctx.do_one_entry(&root_dir, entry)
        })?;

    // remove root dir
    drop(root_dir);
    std::fs::remove_dir(Path::new(&src_dir))?;

    Ok(())
}
