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
        stat::{
            fchmod, fchmodat, futimens, mkdirat, mknodat, umask, utimensat, FchmodatFlags, Mode, SFlag, UtimensatFlags
        },
        time::TimeSpec,
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
        let ret = unsafe {
            libc::ioctl(
                dest_fd.as_ref().unwrap().as_raw_fd(),
                libc::FICLONE,
                src.fd.as_ref().unwrap().as_raw_fd(),
            )
        };
        Errno::result(ret).context("ioctl(FICLONE)")?;
    }

    // recurse into non-empty directories
    // (on ext4, st_nlink=1 means >65000)
    if src.file_type == FileType::Directory && src.st.st_nlink != 2 {
        // TODO: can use st_nlink as EOF hint for getdents
        walk_dir(&src.fd.unwrap(), &dest_fd.unwrap(), buffer_stack)?;
    }

    Ok(())
}

fn walk_dir(
    src_dirfd: &OwnedFd,
    dest_dirfd: &OwnedFd,
    buffer_stack: &BufferStack,
) -> anyhow::Result<()> {
    for_each_getdents(src_dirfd, buffer_stack, |entry| {
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
    walk_dir(&src_dirfd, &dest_dirfd, &buffer_stack).map_err(|e| anyhow!("{}/{}", src_dir, e))?;

    Ok(())
}
