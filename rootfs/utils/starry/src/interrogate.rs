use std::{
    ffi::CStr,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
};

use nix::{
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use smallvec::{SmallVec, ToSmallVec};

use crate::sys::{
    file::statx,
    getdents::{DirEntry, FileType},
    inode_flags::InodeFlags,
    libc_ext,
    link::with_readlinkat,
    xattr::{for_each_flistxattr, for_each_llistxattr, with_fgetxattr, with_lgetxattr},
};

// TODO: relative path is unsafe if we decide to embed this as a library in another process
//const PROC_SELF_FD_PREFIX: &str = "/proc/self/fd/";
pub const PROC_SELF_FD_PREFIX: &str = "";

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
pub struct DevIno(u64, u64);

/*
 * fifo/char/block/socket: fstatat, llistxattr/lgetxattr
 *   - can't safely get inode flags: that requires opening without O_PATH
 *
 * symlink: fstatat, readlinkat, llistxattr/lgetxattr
 *
 * regular: openat, fstat, flistxattr/fgetxattr
 *
 * dir: openat, fstat, flistxattr/fgetxattr
 */
pub struct InterrogatedFile<'a> {
    dirfd: &'a OwnedFd,
    pub file_type: FileType,
    name: &'a CStr,

    stx: libc_ext::statx,
    pub fd: Option<OwnedFd>,
}

impl<'a> InterrogatedFile<'a> {
    pub fn chdir_to_proc() -> anyhow::Result<()> {
        // to reduce path lookup work for fd paths
        std::env::set_current_dir("/proc/self/fd")?;
        Ok(())
    }

    // given any dirfd and entry, stat the file and open it if applicable, using the most efficient possible combination of syscalls
    pub fn from_entry(dirfd: &'a OwnedFd, entry: &'a DirEntry<'a>) -> anyhow::Result<Self> {
        Self::from_name_and_type(dirfd, entry.name, entry.file_type)
    }

    pub fn from_directory_fd(dirfd: &'a OwnedFd) -> anyhow::Result<Self> {
        Self::from_name_and_type(dirfd, c".", FileType::Directory)
    }

    pub fn from_name_and_type(
        dirfd: &'a OwnedFd,
        name: &'a CStr,
        file_type: FileType,
    ) -> anyhow::Result<Self> {
        // 1. determine file type:
        // do we know the file type for sure? some filesystems populate d_type; many don't
        // if not, we must always start with fstatat, as it's unsafe to try opening char/block/fifo
        let mut stx: Option<libc_ext::statx> = None;
        let file_type = match file_type {
            FileType::Unknown => {
                stx = Some(statx(
                    dirfd,
                    name,
                    libc::AT_NO_AUTOMOUNT | libc::AT_SYMLINK_NOFOLLOW,
                    libc_ext::STATX_BASIC_STATS | libc_ext::STATX_MNT_ID,
                )?);
                FileType::from_stat_fmt(stx.as_ref().unwrap().stx_mode as u32 & libc::S_IFMT)
            }
            _ => file_type,
        };

        // 2. get remaining info, depending on whether it's regular/dir, or special
        let mut opened_fd: Option<OwnedFd> = None;
        if file_type == FileType::Regular || file_type == FileType::Directory {
            // regular/dir: openat, fstat

            let mut oflags = OFlag::O_RDONLY
                | OFlag::O_CLOEXEC
                | OFlag::O_NONBLOCK // in case of race (swapped with unconnected fifo): prevent hang
                | OFlag::O_NOCTTY // in case of race (swapped with tty): prevent kill
                | OFlag::O_NOFOLLOW; // in case of race (swapped with symlink): prevent escape

            // if we know that it's supposed to be a directory, add O_DIRECTORY for safety (to avoid calling getdents on a regular file later)
            if file_type == FileType::Directory {
                oflags |= OFlag::O_DIRECTORY;
            }

            // always open
            let fd = openat(Some(dirfd.as_raw_fd()), name, oflags, Mode::empty())?;
            let fd = unsafe { OwnedFd::from_raw_fd(fd) };

            // fstat if not done earlier
            if stx.is_none() {
                stx = Some(statx(
                    &fd,
                    c"",
                    libc::AT_EMPTY_PATH | libc::AT_NO_AUTOMOUNT | libc::AT_SYMLINK_NOFOLLOW,
                    libc_ext::STATX_BASIC_STATS | libc_ext::STATX_MNT_ID,
                )?);
            }

            opened_fd = Some(fd);
        } else {
            // special: fstatat

            // fstatat if not done earlier
            if stx.is_none() {
                stx = Some(statx(
                    dirfd,
                    name,
                    libc::AT_NO_AUTOMOUNT | libc::AT_SYMLINK_NOFOLLOW,
                    libc_ext::STATX_BASIC_STATS | libc_ext::STATX_MNT_ID,
                )?);
            }
        }

        Ok(Self {
            dirfd,
            file_type,
            name,
            stx: stx.unwrap(),
            fd: opened_fd,
        })
    }

    // all file types
    pub fn permissions(&self) -> Mode {
        Mode::from_bits_retain(self.stx.stx_mode as u32 & !libc::S_IFMT)
    }
    pub fn uid(&self) -> u32 {
        self.stx.stx_uid
    }
    pub fn gid(&self) -> u32 {
        self.stx.stx_gid
    }
    pub fn atime(&self) -> (i64, u32) {
        (self.stx.stx_atime.tv_sec, self.stx.stx_atime.tv_nsec)
    }
    pub fn mtime(&self) -> (i64, u32) {
        (self.stx.stx_mtime.tv_sec, self.stx.stx_mtime.tv_nsec)
    }
    pub fn apparent_size(&self) -> u64 {
        self.stx.stx_size
    }
    pub fn actual_size(&self) -> u64 {
        self.stx.stx_blocks * 512
    }
    pub fn is_maybe_sparse(&self) -> bool {
        self.actual_size() < self.apparent_size()
    }
    pub fn block_size(&self) -> u32 {
        self.stx.stx_blksize
    }
    pub fn mnt_id(&self) -> u64 {
        self.stx.stx_mnt_id
    }
    fn nlink(&self) -> u32 {
        self.stx.stx_nlink
    }

    // char/block
    pub fn device_major_minor(&self) -> (u32, u32) {
        (self.stx.stx_dev_major, self.stx.stx_dev_minor)
    }
    pub fn device_rdev(&self) -> u64 {
        let (major, minor) = self.device_major_minor();
        libc::makedev(major, minor)
    }

    // all file types
    fn attr(&self, bit: i32) -> bool {
        self.stx.stx_attributes & bit as u64 != 0
    }
    pub fn inode_flags(&self) -> anyhow::Result<InodeFlags> {
        let mut flags = InodeFlags::empty();
        flags.set(
            InodeFlags::COMPR,
            self.attr(libc_ext::STATX_ATTR_COMPRESSED),
        );
        flags.set(
            InodeFlags::IMMUTABLE,
            self.attr(libc_ext::STATX_ATTR_IMMUTABLE),
        );
        flags.set(InodeFlags::APPEND, self.attr(libc_ext::STATX_ATTR_APPEND));
        flags.set(InodeFlags::NODUMP, self.attr(libc_ext::STATX_ATTR_NODUMP));
        flags.set(
            InodeFlags::ENCRYPT,
            self.attr(libc_ext::STATX_ATTR_ENCRYPTED),
        );
        flags.set(InodeFlags::VERITY, self.attr(libc_ext::STATX_ATTR_VERITY));
        flags.set(InodeFlags::DAX, self.attr(libc_ext::STATX_ATTR_DAX));
        Ok(flags)
    }

    // valid for non-directories only
    // (yes, you can hard link a symlink!)
    pub fn is_hardlink(&self) -> bool {
        self.file_type != FileType::Directory && self.nlink() > 1
    }

    // valid for any file type
    pub fn dev_ino(&self) -> DevIno {
        let dev = libc::makedev(self.stx.stx_dev_major, self.stx.stx_dev_minor);
        DevIno(dev, self.stx.stx_ino)
    }

    // must only be called if file_type == FileType::Symlink
    pub fn with_readlink<T>(&self, f: impl FnOnce(&[u8]) -> T) -> anyhow::Result<T> {
        Ok(with_readlinkat(self.dirfd, self.name, f)?)
    }

    // can be called on any file type
    pub fn for_each_xattr(
        &self,
        mut f: impl FnMut(&CStr, &[u8]) -> nix::Result<()>,
    ) -> anyhow::Result<()> {
        if let Some(ref fd) = self.fd {
            // if we have an open fd, it's easy
            for_each_flistxattr(fd, |key| with_fgetxattr(fd, key, |value| f(key, value)))?;
        } else {
            // otherwise, we have to use /proc/self/fd/<dirfd>/<name>
            // flistxattr/fgetxattr doesn't work on O_PATH fds, so the only alternative would be to use a full absolute path, which is slow and requires keeping track of absolute paths when recursing

            with_fd_path(self.dirfd, Some(self.name), |path_cstr| {
                for_each_llistxattr(path_cstr, |key| {
                    with_lgetxattr(path_cstr, key, |value| f(key, value))
                })
            })?;
        }

        Ok(())
    }
}

pub fn with_fd_path<T, F: AsRawFd>(
    dirfd: &F,
    name: Option<&CStr>,
    f: impl FnOnce(&CStr) -> T,
) -> T {
    // all of this is a fancy zero-allocation way to do format!("/proc/self/fd/{}/{}\0", dirfd, name)
    let mut num_buf = itoa::Buffer::new();
    let formatted_fd = num_buf.format(dirfd.as_raw_fd());

    // string formating go brrr
    let mut path_buf: SmallVec<[u8; 1024]> = PROC_SELF_FD_PREFIX.as_bytes().to_smallvec();
    path_buf.extend_from_slice(formatted_fd.as_bytes());
    if let Some(name) = name {
        path_buf.push(b'/');
        path_buf.extend_from_slice(name.to_bytes_with_nul());
    } else {
        path_buf.push(b'\0');
    }

    // with &[u8] instead of &CStr, with_nix_path attempts to add a null terminator
    let path_cstr = unsafe { CStr::from_bytes_with_nul_unchecked(&path_buf) };
    f(path_cstr)
}
