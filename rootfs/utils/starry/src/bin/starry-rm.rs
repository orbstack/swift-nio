use std::{ffi::CStr, os::fd::{AsRawFd, FromRawFd, OwnedFd}, path::Path};

use anyhow::anyhow;
use nix::{errno::Errno, fcntl::{openat, OFlag}, sys::stat::Mode};
use starry::sys::{file::{fstatat, unlinkat}, getdents::{for_each_getdents, DirEntry}, inode_flags::InodeFlags};

fn clear_flags(fd: &OwnedFd) -> anyhow::Result<bool> {
    let mut flags = InodeFlags::from_file(fd)?;
    if flags.intersects(InodeFlags::IMMUTABLE | InodeFlags::APPEND) {
        flags.remove(InodeFlags::IMMUTABLE | InodeFlags::APPEND);
        flags.apply(fd)?;
        Ok(true)
    } else {
        Ok(false)
    }
}

fn unlinkat_and_clear_flags(dirfd: &OwnedFd, path: &CStr, unlink_flags: i32) -> anyhow::Result<()> {
    // common case: try plain unlink first
    match unlinkat(dirfd, path, unlink_flags) {
        Ok(_) => Ok(()),
        Err(Errno::EPERM) => {
            // on EPERM (not EACCES), try to clear flags and remove again
            let parent_cleared = clear_flags(dirfd)?;

            // both parent and child flags will prevent deletion
            // O_PATH doesn't work and returns EBADF :(
            let fd = unsafe { OwnedFd::from_raw_fd(openat(Some(dirfd.as_raw_fd()), path, OFlag::O_RDONLY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
            let child_cleared = clear_flags(&fd)?;
            drop(fd);

            if child_cleared || parent_cleared {
                unlinkat(dirfd, path, unlink_flags)?;
                Ok(())
            } else {
                Err(Errno::EPERM.into())
            }
        },
        Err(e) => Err(e.into()),
    }
}

fn do_one_entry(dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
    // TODO: minor optimization: we will open dirs anyway, so can fstat after open
    let st = fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?;
    let typ = st.st_mode & libc::S_IFMT;
    if typ == libc::S_IFDIR {
        // dir
        let child_dirfd = unsafe { OwnedFd::from_raw_fd(openat(Some(dirfd.as_raw_fd()), entry.name, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
        walk_dir(&child_dirfd)?;

        unlinkat_and_clear_flags(dirfd, entry.name, libc::AT_REMOVEDIR)?;
    } else {
        // file, symlink, fifo, chr, blk, socket
        unlinkat_and_clear_flags(dirfd, entry.name, 0)?;
    }

    Ok(())
}

fn walk_dir(dirfd: &OwnedFd) -> anyhow::Result<()> {
    for_each_getdents(dirfd, |entry| {
        do_one_entry(dirfd, &entry)
            .map_err(|e| {
                if e.is::<nix::Error>() {
                    // nix::Error = root cause
                    // start chain: "PATH: ERROR"
                    anyhow!("{}: {}", entry.name.to_string_lossy(), e)
                } else {
                    // as we unwind the directory stack, prepend dirs to error
                    // chain: "DIR/CHILD: ERROR"
                    anyhow!("{}/{}", entry.name.to_string_lossy(), e)
                }
            })
    })?;

    Ok(())
}

fn main() -> anyhow::Result<()> {
    // open root dir
    let src_dir = std::env::args().nth(1).ok_or_else(|| anyhow!("missing src dir"))?;
    let root_dir = unsafe { OwnedFd::from_raw_fd(openat(None, Path::new(&src_dir), OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };

    // walk dirs
    walk_dir(&root_dir)
        .map_err(|e| anyhow!("{}/{}", src_dir, e))?;

    // remove root dir
    std::fs::remove_dir(Path::new(&src_dir))?;

    Ok(())
}
