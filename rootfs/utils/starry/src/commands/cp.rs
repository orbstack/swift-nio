/*
 * starry cp
 * similar to `cp -raf --reflink=auto`
 *
 * features:
 * - supports xattrs (unlike busybox cp), even on symlinks
 * - supports inode flags/attributes like immutable and append-only (unlike any cp)
 * - supports hard links (unlike any cp)
 * - supports sockets (unlike any cp)
 * - uses reflinks when possible
 * - supports sparse files
 * - supports fifos and char/block devices
 * - safe against symlink races (everything is dirfd/O_NOFOLLOW)
 *
 * assumptions:
 * - source is NOT modified concurrently. if this is violated, the command may fail or produce inconsistent results, but there is no security risk (in the case of symlink races). specifically, deletion races may cause the entire command to fail.
 * - should be run as root in order to set trusted.* xattrs, inode flags, and other xattrs on symlinks
 */

use std::{
    collections::{btree_map::Entry, BTreeMap},
    ffi::{CStr, CString},
    os::{
        fd::{AsRawFd, FromRawFd, OwnedFd},
        unix::{fs::fchown, net::UnixListener},
    },
    path::Path,
};

use crate::{
    interrogate::{with_fd_path, DevIno, InterrogatedFile, PROC_SELF_FD_PREFIX},
    recurse::Recurser,
    sys::{
        file::{fchownat, AT_FDCWD},
        getdents::{DirEntry, FileType},
        inode_flags::InodeFlags,
        libc_ext,
        link::with_readlinkat,
        xattr::{fsetxattr, lsetxattr},
    },
};
use anyhow::Context;
use bumpalo::Bump;
use libc::{getegid, geteuid};
use nix::{
    errno::Errno,
    fcntl::{copy_file_range, openat, AtFlags, OFlag},
    sys::{
        sendfile::sendfile,
        stat::{
            fchmod, fchmodat, futimens, mkdirat, mknodat, umask, utimensat, FchmodatFlags, Mode,
            SFlag, UtimensatFlags,
        },
        time::TimeSpec,
    },
    unistd::{ftruncate, linkat, lseek, mkfifoat, symlinkat, Whence},
};

struct OwnedCopyContext {
    bump: Bump,
    recurser: Recurser,
}

impl OwnedCopyContext {
    fn new() -> anyhow::Result<Self> {
        Ok(Self {
            bump: Bump::new(),
            recurser: Recurser::new()?,
        })
    }
}

struct CopyContext<'a> {
    euid: u32,
    egid: u32,

    // no self-referential lifetimes :(
    hardlink_paths: BTreeMap<DevIno, &'a [u8]>,
    bump: &'a Bump,

    recurser: &'a Recurser,
}

impl<'a> CopyContext<'a> {
    fn new(owned: &'a OwnedCopyContext) -> anyhow::Result<Self> {
        Ok(Self {
            euid: unsafe { geteuid() },
            egid: unsafe { getegid() },

            hardlink_paths: BTreeMap::new(),
            bump: &owned.bump,

            recurser: &owned.recurser,
        })
    }

    fn copy_metadata_to_fd(&self, src: &InterrogatedFile, fd: &OwnedFd) -> anyhow::Result<()> {
        // we have an open fd; use it for perf

        // only call fchown if different from current fsuid/fsgid
        // TODO: is changing fsuid/fsgid before creation faster?
        if src.uid() != self.euid || src.gid() != self.egid {
            fchown(fd, Some(src.uid()), Some(src.gid())).context("fchown")?;
        }

        // suid/sgid gets cleared after chown
        let src_perm = src.permissions();
        if src_perm.contains(Mode::S_ISUID) || src_perm.contains(Mode::S_ISGID) {
            fchmod(fd.as_raw_fd(), src_perm).context("fchmod")?;
        }

        src.for_each_xattr(|key, value| fsetxattr(fd, key, value, 0))
            .context("listxattr/setxattr")?;

        // do this last, in case other operations would change mtime
        // no point in doing this lazily: with nsec, it'll never match src
        let (atime, atime_nsec) = src.atime();
        let (mtime, mtime_nsec) = src.mtime();
        futimens(
            fd.as_raw_fd(),
            &TimeSpec::new(atime, atime_nsec as i64),
            &TimeSpec::new(mtime, mtime_nsec as i64),
        )
        .context("futimens")?;

        // inode flags
        // must be last due to immutable/append-only flags (which even prevent mtime changes)
        // this doesn't change mtime, so it's safe to do after utimens()
        // filter to flags that are included in tar archives
        // otherwise we'll be setting flags on every file if btrfs has nocow/compress enabled
        let flags = src.inode_flags()?.intersection(InodeFlags::ARCHIVE_FLAGS);
        if !flags.is_empty() {
            flags.apply(fd).context("ioctl(FS_IOC_SETFLAGS)")?;
        }

        Ok(())
    }

    fn copy_metadata_to_dirfd_path(
        &self,
        src: &InterrogatedFile,
        dest_dirfd: &OwnedFd,
        dest_name: &CStr,
    ) -> anyhow::Result<()> {
        if src.uid() != self.euid || src.gid() != self.egid {
            fchownat(
                dest_dirfd,
                dest_name,
                src.uid(),
                src.gid(),
                libc::AT_SYMLINK_NOFOLLOW,
            )
            .context("fchownat")?;
        }

        let src_perm = src.permissions();
        if src_perm.contains(Mode::S_ISUID) || src_perm.contains(Mode::S_ISGID) {
            fchmodat(
                Some(dest_dirfd.as_raw_fd()),
                dest_name,
                src_perm,
                FchmodatFlags::NoFollowSymlink,
            )
            .context("fchmodat")?;
        }

        src.for_each_xattr(|key, value| {
            // slowpath: most files only have 0 or 1 xattrs, so need to dedupe fd path creation
            with_fd_path(dest_dirfd, Some(dest_name), |path_cstr| {
                lsetxattr(path_cstr, key, value, 0)
            })
        })
        .context("listxattr/setxattr")?;

        let (atime, atime_nsec) = src.atime();
        let (mtime, mtime_nsec) = src.mtime();
        utimensat(
            Some(dest_dirfd.as_raw_fd()),
            dest_name,
            &TimeSpec::new(atime, atime_nsec as i64),
            &TimeSpec::new(mtime, mtime_nsec as i64),
            UtimensatFlags::NoFollowSymlink,
        )
        .context("utimensat")?;

        Ok(())
    }

    fn do_one_entry(
        &mut self,
        src_dirfd: &OwnedFd,
        dest_dirfd: &OwnedFd,
        entry: &DirEntry,
    ) -> anyhow::Result<()> {
        let src = InterrogatedFile::from_entry(src_dirfd, entry)?;

        // handle hard links
        if src.is_hardlink() {
            match self.hardlink_paths.entry(src.dev_ino()) {
                Entry::Vacant(v) => {
                    // this is the first time we've seen this dev/ino
                    // add current path to hardlink map and continue adding file contents to the archive
                    // this (sadly) allocates and uses a syscall to stat /proc/self/fd, but it's a slowpath for st_nlink>1: hardlinks are rare, and we optimize it with bump allocation when we do need it
                    // keeping track of paths in a PathStack slows down the common case

                    with_fd_path(dest_dirfd, None, |fd_path| {
                        with_readlinkat(AT_FDCWD, fd_path, |parent_path| {
                            // concat: parent_path + '/' + entry.name
                            let file_path = self.bump.alloc_slice_fill_default(
                                parent_path.len() + entry.name.count_bytes() + 1,
                            );
                            file_path[..parent_path.len()].copy_from_slice(parent_path);
                            file_path[parent_path.len()] = b'/';
                            file_path[parent_path.len() + 1..]
                                .copy_from_slice(entry.name.to_bytes());

                            v.insert(file_path);
                        })
                    })?;
                }
                Entry::Occupied(o) => {
                    // not the first time! hard link it and move on
                    let old_path = o.get();

                    // this looks potentially racy in the presence of concurrent symlink modifications in the src dir (because linkat() with an absolute/multi-component src path could follow symlinks),
                    // but it's actually safe because all our hardlink src paths are relative to the *dest* dir, which cp is guaranteed to have exclusive access to. (it fails if mkdirat returns EEXIST.)
                    linkat(
                        None,
                        *old_path,
                        Some(dest_dirfd.as_raw_fd()),
                        entry.name.to_bytes(),
                        AtFlags::empty(),
                    )
                    .context("linkat")?;

                    return Ok(());
                }
            }
        }

        // create dest
        let src_perm = src.permissions();
        let dest_fd = match src.file_type {
            // simple device types
            FileType::Fifo => {
                mkfifoat(Some(dest_dirfd.as_raw_fd()), entry.name, src_perm).context("mkfifoat")?;
                None
            }
            FileType::Block => {
                mknodat(
                    Some(dest_dirfd.as_raw_fd()),
                    entry.name,
                    SFlag::S_IFBLK,
                    src_perm,
                    src.device_rdev(),
                )
                .context("mknodat")?;
                None
            }
            FileType::Char => {
                mknodat(
                    Some(dest_dirfd.as_raw_fd()),
                    entry.name,
                    SFlag::S_IFCHR,
                    src_perm,
                    src.device_rdev(),
                )
                .context("mknodat")?;
                None
            }

            // more complicated special types: symlink, socket
            FileType::Symlink => {
                src.with_readlink(|link_path| {
                    symlinkat(link_path, Some(dest_dirfd.as_raw_fd()), entry.name)
                })
                .context("readlink")?
                .context("symlinkat")?;
                None
            }
            FileType::Socket => {
                // sockets are uncommon, so this can be slow (String allocation + extra listen syscall)
                let fd_path = format!(
                    "{}{}/{}",
                    PROC_SELF_FD_PREFIX,
                    dest_dirfd.as_raw_fd(),
                    entry.name.to_string_lossy()
                );
                _ = UnixListener::bind(&fd_path)?;

                // can't specify mode as part of bind/listen
                fchmodat(
                    Some(dest_dirfd.as_raw_fd()),
                    entry.name,
                    src_perm,
                    FchmodatFlags::NoFollowSymlink,
                )
                .context("fchmodat")?;

                None
            }

            // regular files and directories
            FileType::Regular => {
                let fd = openat(
                    Some(dest_dirfd.as_raw_fd()),
                    entry.name,
                    OFlag::O_CREAT | OFlag::O_WRONLY | OFlag::O_CLOEXEC,
                    src_perm,
                )
                .context("openat")?;
                let fd = unsafe { OwnedFd::from_raw_fd(fd) };
                Some(fd)
            }
            FileType::Directory => {
                mkdirat(Some(dest_dirfd.as_raw_fd()), entry.name, src_perm).context("mkdirat")?;

                // TODO: empty dirs don't need to be opened if no inode flags
                let fd = openat(
                    Some(dest_dirfd.as_raw_fd()),
                    entry.name,
                    OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
                    src_perm,
                )
                .context("openat")?;
                let fd = unsafe { OwnedFd::from_raw_fd(fd) };
                Some(fd)
            }

            FileType::Unknown => unreachable!(),
        };

        // file contents: only support reflinking
        // must do this before immutable/append-only flags are set
        // also, must do this before metadata is copied, otherwise we'll break the mtime
        if src.file_type == FileType::Regular {
            copy_regular_file_contents(&src, src.fd.as_ref().unwrap(), dest_fd.as_ref().unwrap())?;
        }

        // recurse into directories
        if src.file_type == FileType::Directory {
            let src_dirfd = src.fd.as_ref().unwrap();
            self.recurser.walk_dir(src_dirfd, |entry| {
                self.do_one_entry(src_dirfd, dest_dirfd, entry)
            })?;
        }

        // metadata: uid/gid, atime/mtime, xattrs, inode flags
        // must be after copying contents/children, because that updates mtime
        // (and we can't copy files into a directory that's marked immutable, or a dir with no owner write perms)
        if let Some(ref fd) = dest_fd {
            // we have an open fd; use it for perf
            self.copy_metadata_to_fd(&src, fd)?;
        } else {
            // don't have fd; fall back to path
            // TODO: faster to open(O_PATH)?
            self.copy_metadata_to_dirfd_path(&src, dest_dirfd, entry.name)?;
        }

        // close_range for src and dest dir fds
        if let Some(src_fd) = src.fd {
            if let Some(dest_fd) = dest_fd {
                close_fds(src_fd, dest_fd);
            }
        }

        Ok(())
    }
}

// small optimization: src and dest fd numbers are always contiguous unless we're in a multi-threaded program (which the CLI will never be) or have debug/other code running in between that opens fds
// so we can use close_range to close them in a single syscall
fn close_fds(mut a: OwnedFd, mut b: OwnedFd) {
    if a.as_raw_fd() > b.as_raw_fd() {
        std::mem::swap(&mut a, &mut b);
    }

    if a.as_raw_fd() + 1 == b.as_raw_fd() {
        let ret = unsafe { libc_ext::close_range(a.as_raw_fd() as u32, b.as_raw_fd() as u32, 0) };
        if ret != 0 {
            panic!("close_range failed: {}", ret);
        }

        std::mem::forget(a);
        std::mem::forget(b);
    }

    // drops OwnedFd for normal close in non-contiguous case
}

fn copy_file_region(
    src_fd: &OwnedFd,
    dest_fd: &OwnedFd,
    off: i64,
    mut rem: u64,
) -> nix::Result<()> {
    let mut off_in = off;
    let mut off_out = off;
    while rem > 0 {
        // syscall increments off for us
        let ret = copy_file_range(
            src_fd,
            Some(&mut off_in),
            dest_fd,
            Some(&mut off_out),
            rem as usize,
        )?;
        if ret == 0 {
            // EOF: race (file got smaller since stat)
            break;
        }

        rem -= ret as u64;
    }

    Ok(())
}

fn copy_file_region_sendfile(
    src_fd: &OwnedFd,
    dest_fd: &OwnedFd,
    mut off: i64,
    mut rem: u64,
) -> nix::Result<()> {
    while rem > 0 {
        // syscall increments off for us
        // however, sendfile only supports a src offset, so we have to seek dest manually
        lseek(dest_fd.as_raw_fd(), off, Whence::SeekSet)?;
        let ret = sendfile(dest_fd, src_fd, Some(&mut off), rem as usize)?;
        if ret == 0 {
            // EOF: race (file got smaller since stat)
            break;
        }

        rem -= ret as u64;
    }

    Ok(())
}

fn copy_regular_file_contents(
    src_file: &InterrogatedFile,
    src_fd: &OwnedFd,
    dest_fd: &OwnedFd,
) -> anyhow::Result<()> {
    // 1. attempt ioctl(FICLONE) for copy-on-write reflink
    let ret = unsafe { libc::ioctl(dest_fd.as_raw_fd(), libc::FICLONE as _, src_fd.as_raw_fd()) };
    match Errno::result(ret) {
        Ok(_) => return Ok(()),
        // various cases of "not supported"
        // sadly, this is even possible on btrfs due to compression(?) / swapfiles
        Err(
            Errno::ENOTTY
            | Errno::EBADF
            | Errno::EINVAL
            | Errno::EOPNOTSUPP
            | Errno::ETXTBSY
            | Errno::EXDEV,
        ) => {}
        // don't retry on other errors like ENOSPC: those are real problems
        Err(e) => return Err(e).context("ioctl(FICLONE)"),
    }

    // 2. fall back to copy_file_range/sendfile
    // if we know that the file isn't sparse, skip lseek
    if !src_file.is_maybe_sparse() {
        match copy_file_region(src_fd, dest_fd, 0, src_file.apparent_size()) {
            Ok(_) => return Ok(()),
            // not supported
            Err(Errno::EOPNOTSUPP | Errno::ETXTBSY | Errno::EXDEV) => {}
            Err(e) => return Err(e.into()),
        };

        copy_file_region_sendfile(src_fd, dest_fd, 0, src_file.apparent_size())?;
        return Ok(());
    }

    // sparse case:
    // - lseek(SEEK_DATA) to find the next data start (file may start with a hole)
    // - lseek(SEEK_HOLE) to find the next hole
    let mut off: i64 = 0;
    let mut use_sendfile = false;
    while off < src_file.apparent_size() as i64 {
        let data_start = match lseek(src_fd.as_raw_fd(), off, Whence::SeekData) {
            Ok(data_start) => data_start,
            // file has no (more) data
            Err(Errno::ENXIO) => break,
            Err(e) => return Err(e.into()),
        };
        let hole_start = match lseek(src_fd.as_raw_fd(), data_start, Whence::SeekHole) {
            Ok(hole_start) => hole_start,
            // file got smaller
            Err(Errno::ENXIO) => break,
            Err(e) => return Err(e.into()),
        };
        let data_len = hole_start - data_start;

        // stop attempting copy_file_region if we know it's not supported
        if use_sendfile {
            copy_file_region_sendfile(src_fd, dest_fd, data_start, data_len as u64)?;
        } else {
            match copy_file_region(src_fd, dest_fd, data_start, data_len as u64) {
                Ok(_) => {}
                Err(Errno::EOPNOTSUPP | Errno::ETXTBSY | Errno::EXDEV) => {
                    use_sendfile = true;
                    copy_file_region_sendfile(src_fd, dest_fd, data_start, data_len as u64)?;
                }
                Err(e) => return Err(e.into()),
            }
        }

        off = hole_start;
    }

    // set size in case file ends with a big hole
    if off < src_file.apparent_size() as i64 {
        ftruncate(dest_fd, src_file.apparent_size() as i64)?;
    }

    Ok(())
}

pub fn main(src_dir: &str, dest_dir: &str) -> anyhow::Result<()> {
    // we need control over all permissions bits
    umask(Mode::empty());

    // open root dir
    let src_dirfd = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            Path::new(&src_dir),
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };

    // interrogate src and copy early metadata
    let src_file = InterrogatedFile::from_directory_fd(&src_dirfd)?;
    let dest_dir_cstr = CString::new(dest_dir)?;
    mkdirat(None, dest_dir_cstr.as_ref(), src_file.permissions()).context("mkdirat")?;

    let dest_dirfd = unsafe {
        OwnedFd::from_raw_fd(openat(
            None,
            dest_dir_cstr.as_ref(),
            OFlag::O_RDONLY | OFlag::O_DIRECTORY | OFlag::O_CLOEXEC,
            Mode::empty(),
        )?)
    };

    // only chdir after opening paths, in case they're relative
    InterrogatedFile::chdir_to_proc()?;

    // walk dirs
    let owned_ctx = OwnedCopyContext::new()?;
    let mut ctx = CopyContext::new(&owned_ctx)?;
    ctx.recurser
        .walk_dir_root(&src_dirfd, &dest_dir_cstr, |entry| {
            ctx.do_one_entry(&src_dirfd, &dest_dirfd, entry)
        })?;

    // to avoid bumping mtime, copy metadata to root dir after recursing
    ctx.copy_metadata_to_dirfd_path(&src_file, &dest_dirfd, c".")?;

    Ok(())
}
