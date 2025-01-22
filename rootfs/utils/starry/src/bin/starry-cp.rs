use std::{
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd},
        unix::{fs::fchown, net::UnixListener},
    },
    path::Path,
};

use anyhow::{anyhow, Context};
use nix::{
    errno::Errno,
    fcntl::{openat, OFlag},
    sys::{
        sendfile::sendfile, stat::{
            fchmod, fchmodat, futimens, mkdirat, mknodat, umask, utimensat, FchmodatFlags, Mode, SFlag, UtimensatFlags
        }, time::TimeSpec
    },
    unistd::{mkfifoat, symlinkat},
};
use starry::{
    buffer_stack::BufferStack,
    interrogate::{with_fd_path, InterrogatedFile},
    sys::{
        file::fchownat, getdents::{for_each_getdents, DirEntry, FileType}, inode_flags::InodeFlags, xattr::{fsetxattr, lsetxattr}
    },
};

fn copy_regular_file_contents(src_st: &libc::stat, src_fd: &OwnedFd, dest_fd: &OwnedFd) -> anyhow::Result<()> {
    // 1. attempt ioctl(FICLONE) for copy-on-write reflink
    let ret = unsafe {
        libc::ioctl(
            dest_fd.as_raw_fd(),
            libc::FICLONE,
            src_fd.as_raw_fd(),
        )
    };
    match Errno::result(ret) {
        Ok(_) => return Ok(()),
        // various cases of "not supported by FS"
        // sadly, this is even possible on btrfs due to compression(?) / swapfiles
        Err(Errno::ENOTTY | Errno::EBADF | Errno::EINVAL | Errno::EOPNOTSUPP | Errno::ETXTBSY | Errno::EXDEV) => {}
        // don't retry on other errors like ENOSPC: those are real problems
        Err(e) => return Err(e).context("ioctl(FICLONE)"),
    }

    // copy_file_range isn't worth trying on btrfs: it also can't copy swapfiles (EXTBSY)

    // fallback doesn't support sparse files
    if src_st.st_blocks * 512 < src_st.st_size {
        return Err(anyhow!("sparse files are not supported on non-CoW filesystems"));
    }

    // 2. fall back to sendfile
    let mut rem = src_st.st_size;
    while rem > 0 {
        let ret = sendfile(dest_fd, src_fd, None, rem as usize)
            .context("sendfile")?;
        if ret == 0 {
            // EOF: race (file got smaller since stat)
            break;
        }

        rem -= ret as i64;
    }

    Ok(())
}

fn do_one_entry(
    src_dirfd: &OwnedFd,
    dest_dirfd: &OwnedFd,
    entry: &DirEntry,
    buffer_stack: &BufferStack,
) -> anyhow::Result<()> {
    let src = InterrogatedFile::from_entry(src_dirfd, entry)?;

    // create dest
    let permissions = Mode::from_bits_retain(src.st.st_mode & !libc::S_IFMT);
    let dest_fd = match src.file_type {
        // simple device types
        FileType::Fifo => {
            mkfifoat(Some(dest_dirfd.as_raw_fd()), entry.name, permissions)
                .context("mkfifoat")?;
            None
        }
        FileType::Block => {
            mknodat(
                Some(dest_dirfd.as_raw_fd()),
                entry.name,
                SFlag::S_IFBLK,
                permissions,
                src.st.st_rdev,
            ).context("mknodat")?;
            None
        }
        FileType::Char => {
            mknodat(
                Some(dest_dirfd.as_raw_fd()),
                entry.name,
                SFlag::S_IFCHR,
                permissions,
                src.st.st_rdev,
            ).context("mknodat")?;
            None
        }

        // more complicated special types: symlink, socket
        FileType::Symlink => {
            src.with_readlink(|link_path| {
                symlinkat(link_path, Some(dest_dirfd.as_raw_fd()), entry.name)
            }).context("readlink")?.context("symlinkat")?;
            None
        }
        FileType::Socket => {
            // sockets are uncommon, so this can be slow (String allocation + extra listen syscall)
            let fd_path = format!(
                "/proc/self/fd/{}/{}",
                dest_dirfd.as_raw_fd(),
                entry.name.to_string_lossy()
            );
            _ = UnixListener::bind(&fd_path)?;

            // can't specify mode as part of bind/listen
            fchmodat(
                Some(dest_dirfd.as_raw_fd()),
                entry.name,
                permissions,
                FchmodatFlags::NoFollowSymlink,
            ).context("fchmodat")?;

            None
        }

        // regular files and directories
        FileType::Regular => {
            let fd = openat(
                Some(dest_dirfd.as_raw_fd()),
                entry.name,
                OFlag::O_CREAT | OFlag::O_WRONLY | OFlag::O_CLOEXEC,
                permissions,
            ).context("openat")?;
            let fd = unsafe { OwnedFd::from_raw_fd(fd) };
            Some(fd)
        }
        FileType::Directory => {
            mkdirat(Some(dest_dirfd.as_raw_fd()), entry.name, permissions)
                .context("mkdirat")?;

            // TODO: empty dirs don't need to be opened if no inode flags
            let fd = openat(
                Some(dest_dirfd.as_raw_fd()),
                entry.name,
                OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
                permissions,
            ).context("openat")?;
            let fd = unsafe { OwnedFd::from_raw_fd(fd) };
            Some(fd)
        }

        FileType::Unknown => unreachable!(),
    };

    // metadata: uid/gid, atime/mtime, xattrs, inode flags
    if let Some(ref fd) = dest_fd {
        // we have an open fd; use it for perf

        // TODO: skip if already matching current fsuid/fsgid. or is changing fsuid/fsgid faster?
        fchown(fd, Some(src.st.st_uid), Some(src.st.st_gid))
            .context("fchown")?;

        // no point in doing this lazily: with nsec, it'll never match src
        futimens(
            fd.as_raw_fd(),
            &TimeSpec::new(src.st.st_atime, src.st.st_atime_nsec),
            &TimeSpec::new(src.st.st_mtime, src.st.st_mtime_nsec),
        ).context("futimens")?;

        // suid/sgid gets cleared after chown
        if permissions.contains(Mode::S_ISUID) || permissions.contains(Mode::S_ISGID) {
            fchmod(fd.as_raw_fd(), permissions)
                .context("fchmod")?;
        }

        src.for_each_xattr(|name, value| {
            fsetxattr(fd, name, value, 0)
        }).context("listxattr/setxattr")?;

        if let Some(flags) = src.inode_flags()? {
            // filter to flags that are included in tar archives
            // otherwise we'll be setting flags on every file if btrfs has nocow/compress enabled
            let fl = flags.intersection(InodeFlags::ARCHIVE_FLAGS);
            if !fl.is_empty() {
                fl.apply(fd).context("ioctl(FS_IOC_SETFLAGS)")?;
            }
        }
    } else {
        // don't have fd; fall back to path
        // TODO: faster to open(O_PATH)?

        fchownat(
            dest_dirfd,
            entry.name,
            src.st.st_uid,
            src.st.st_gid,
            libc::AT_SYMLINK_NOFOLLOW,
        ).context("fchownat")?;

        // no point in doing this lazily: with nsec, it'll never match src
        utimensat(
            Some(dest_dirfd.as_raw_fd()),
            entry.name,
            &TimeSpec::new(src.st.st_atime, src.st.st_atime_nsec),
            &TimeSpec::new(src.st.st_mtime, src.st.st_mtime_nsec),
            UtimensatFlags::NoFollowSymlink,
        ).context("utimensat")?;

        // suid/sgid gets cleared after chown
        if permissions.contains(Mode::S_ISUID) || permissions.contains(Mode::S_ISGID) {
            fchmodat(
                Some(dest_dirfd.as_raw_fd()),
                entry.name,
                permissions,
                FchmodatFlags::NoFollowSymlink,
            ).context("fchmodat")?;
        }

        src.for_each_xattr(|name, value| {
            // slowpath: most files only have 0 or 1 xattrs, so need to dedupe fd path creation
            with_fd_path(dest_dirfd, entry.name, |path_cstr| {
                lsetxattr(path_cstr, name, value, 0)
            })
        }).context("listxattr/setxattr")?;
    }

    // file contents: only support reflinking
    if src.file_type == FileType::Regular {
        copy_regular_file_contents(&src.st, src.fd.as_ref().unwrap(), dest_fd.as_ref().unwrap())?;
    }

    // recurse into non-empty directories
    if src.has_children() {
        walk_dir(src.fd.as_ref().unwrap(), src.nents_hint(), dest_fd.as_ref().unwrap(), buffer_stack)?;
    }

    Ok(())
}

fn walk_dir(
    src_dirfd: &OwnedFd,
    src_nents_hint: Option<usize>,
    dest_dirfd: &OwnedFd,
    buffer_stack: &BufferStack,
) -> anyhow::Result<()> {
    for_each_getdents(src_dirfd, src_nents_hint, buffer_stack, |entry| {
        do_one_entry(src_dirfd, dest_dirfd, &entry, buffer_stack).map_err(|e| {
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
    // we need control over all permissions bits
    umask(Mode::empty());

    // open root dir
    let src_dir = std::env::args()
        .nth(1)
        .ok_or_else(|| anyhow!("missing src dir"))?;
    let dest_dir = std::env::args()
        .nth(2)
        .ok_or_else(|| anyhow!("missing dst dir"))?;

    let src_dirfd = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&src_dir),
            OFlag::O_RDONLY
                | OFlag::O_DIRECTORY
                | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };
    // TODO: create and set metadata on root dir
    let dest_dirfd = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&dest_dir),
            OFlag::O_RDONLY
                | OFlag::O_DIRECTORY
                | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };

    // walk dirs
    let buffer_stack = BufferStack::new()?;
    walk_dir(&src_dirfd, None, &dest_dirfd, &buffer_stack)
        .map_err(|e| anyhow!("{}/{}", src_dir, e))?;

    Ok(())
}
