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
    file::{fstat, fstatat},
    getdents::{DirEntry, FileType},
    inode_flags::InodeFlags,
    link::with_readlinkat,
    xattr::{for_each_flistxattr, for_each_llistxattr, with_fgetxattr, with_lgetxattr},
};

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

    pub st: libc::stat,
    pub fd: Option<OwnedFd>,
}

impl<'a> InterrogatedFile<'a> {
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
        let mut st: Option<libc::stat> = None;
        let file_type = match file_type {
            FileType::Unknown => {
                st = Some(fstatat(dirfd, name, libc::AT_SYMLINK_NOFOLLOW)?);
                FileType::from_stat_fmt(st.as_ref().unwrap().st_mode & libc::S_IFMT)
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
            if st.is_none() {
                st = Some(fstat(&fd)?);
            }

            opened_fd = Some(fd);
        } else {
            // special: fstatat

            // fstatat if not done earlier
            if st.is_none() {
                st = Some(fstatat(dirfd, name, libc::AT_SYMLINK_NOFOLLOW)?);
            }
        }

        Ok(Self {
            dirfd,
            file_type,
            name,
            st: st.unwrap(),
            fd: opened_fd,
        })
    }

    // all file types
    pub fn permissions(&self) -> Mode {
        Mode::from_bits_retain(self.st.st_mode & !libc::S_IFMT)
    }
    pub fn uid(&self) -> u32 {
        self.st.st_uid
    }
    pub fn gid(&self) -> u32 {
        self.st.st_gid
    }
    pub fn atime(&self) -> (i64, u32) {
        (self.st.st_atime, self.st.st_atime_nsec as u32)
    }
    pub fn mtime(&self) -> (i64, u32) {
        (self.st.st_mtime, self.st.st_mtime_nsec as u32)
    }
    pub fn apparent_size(&self) -> u64 {
        self.st.st_size as u64
    }
    pub fn is_maybe_sparse(&self) -> bool {
        self.st.st_blocks < self.st.st_size / 512
    }
    fn nlink(&self) -> u32 {
        self.st.st_nlink
    }

    // char/block
    pub fn device_major_minor(&self) -> (u32, u32) {
        let major = unsafe { libc::major(self.st.st_rdev) };
        let minor = unsafe { libc::minor(self.st.st_rdev) };
        (major, minor)
    }
    pub fn device_rdev(&self) -> u64 {
        self.st.st_rdev
    }

    // can be called on any file type; returns None for regular files and directories
    pub fn inode_flags(&self) -> anyhow::Result<Option<InodeFlags>> {
        let Some(ref fd) = self.fd else {
            return Ok(None);
        };

        // also doesn't work on O_PATH fds :(
        // so we only support regular files and directories for now
        let flags = InodeFlags::from_file(fd)?;
        Ok(Some(flags))
    }

    // valid for any file type
    pub fn has_children(&self) -> bool {
        // on ext4, st_nlink=1 means >65000, so check for != 2
        self.file_type == FileType::Directory && self.nlink() != 2
    }

    // valid for non-directories only
    // (yes, you can hard link a symlink!)
    pub fn is_hardlink(&self) -> bool {
        self.file_type != FileType::Directory && self.nlink() > 1
    }

    // valid for any file type
    pub fn dev_ino(&self) -> DevIno {
        DevIno(self.st.st_dev, self.st.st_ino)
    }

    // only for directories
    pub fn nents_hint(&self) -> Option<usize> {
        // on ext4, st_nlink is supposed to overflow at 65000 and reset to 1
        // to be safe, also consider it invalid as a hint if it's > 64998
        match self.nlink() {
            0..=1 => None,
            // # children = minus "." and ".."
            // however, getdents also returns "." and "..", so we don't subtract 2
            2..=64998 => Some(self.nlink() as usize),
            _ => None,
        }
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
    let mut path_buf: SmallVec<[u8; 1024]> = b"/proc/self/fd/".to_smallvec();
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
