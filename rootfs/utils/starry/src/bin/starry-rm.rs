use std::{ffi::CStr, os::fd::{AsRawFd, FromRawFd, OwnedFd}, path::Path};

use anyhow::anyhow;
use nix::{errno::Errno, fcntl::{openat, OFlag}, sys::stat::Mode};
use starry::sys::{file::{fstatat, unlinkat}, getdents::{for_each_getdents, DirEntry, FileType}, inode_flags::InodeFlags};

fn clear_flags(fd: &OwnedFd) -> nix::Result<bool> {
    let mut flags = InodeFlags::from_file(fd)?;
    if flags.intersects(InodeFlags::IMMUTABLE | InodeFlags::APPEND) {
        flags.remove(InodeFlags::IMMUTABLE | InodeFlags::APPEND);
        flags.apply(fd)?;
        Ok(true)
    } else {
        Ok(false)
    }
}

fn unlinkat_and_clear_flags(dirfd: &OwnedFd, path: &CStr, unlink_flags: i32) -> nix::Result<()> {
    // common case: try plain unlink first
    match unlinkat(dirfd, path, unlink_flags) {
        Ok(_) => Ok(()),
        Err(Errno::EPERM) => {
            // on EPERM (not EACCES), try to clear flags and remove again
            let parent_cleared = match clear_flags(dirfd) {
                Ok(cleared) => cleared,
                Err(e) => {
                    eprintln!("failed to clear flags from parent dir: {}", e);
                    return Err(e);
                },
            };

            // both parent and child flags will prevent deletion
            // O_PATH doesn't work and returns EBADF :(
            let fd = unsafe { OwnedFd::from_raw_fd(openat(Some(dirfd.as_raw_fd()), path, OFlag::O_RDONLY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
            let child_cleared = match clear_flags(&fd) {
                Ok(cleared) => cleared,
                Err(e) => {
                    eprintln!("failed to clear flags from file: {}", e);
                    return Err(e);
                },
            };
            drop(fd);

            if child_cleared || parent_cleared {
                unlinkat(dirfd, path, unlink_flags)?;
                Ok(())
            } else {
                Err(Errno::EPERM)
            }
        },
        Err(e) => Err(e),
    }
}

fn do_one_entry(dirfd: &OwnedFd, entry: &DirEntry) -> anyhow::Result<()> {
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
    let child_dirfd = unsafe { OwnedFd::from_raw_fd(openat(Some(dirfd.as_raw_fd()), entry.name, OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC | OFlag::O_NONBLOCK | OFlag::O_NOCTTY, Mode::empty())?) };
    walk_dir(&child_dirfd)?;
    drop(child_dirfd);

    unlinkat_and_clear_flags(dirfd, entry.name, libc::AT_REMOVEDIR)?;
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
