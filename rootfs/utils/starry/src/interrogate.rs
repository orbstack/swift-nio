use std::{
    ffi::CStr,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
};

use nix::{
    fcntl::{openat, OFlag},
    sys::stat::Mode,
};
use numtoa::NumToA;
use smallvec::{SmallVec, ToSmallVec};

use crate::sys::{
    file::{fstat, fstatat},
    getdents::{DirEntry, FileType},
    inode_flags::InodeFlags,
    link::with_readlinkat,
    xattr::{for_each_flistxattr, for_each_llistxattr, with_fgetxattr, with_lgetxattr},
};

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
        // 1. determine file type:
        // do we know the file type for sure? some filesystems populate d_type; many don't
        // if not, we must always start with fstatat, as it's unsafe to try opening char/block/fifo
        let mut st: Option<libc::stat> = None;
        let file_type = match entry.file_type {
            FileType::Unknown => {
                st = Some(fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?);
                FileType::from_stat_fmt(st.as_ref().unwrap().st_mode & libc::S_IFMT)
            }
            _ => entry.file_type,
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
            let fd = openat(Some(dirfd.as_raw_fd()), entry.name, oflags, Mode::empty())?;
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
                st = Some(fstatat(dirfd, entry.name, libc::AT_SYMLINK_NOFOLLOW)?);
            }
        }

        Ok(Self {
            dirfd,
            file_type: entry.file_type,
            name: entry.name,
            st: st.unwrap(),
            fd: opened_fd,
        })
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
        self.file_type == FileType::Directory && self.st.st_nlink != 2
    }

    // only for directories
    pub fn nents_hint(&self) -> Option<usize> {
        // on ext4, st_nlink is supposed to overflow at 65000 and reset to 1
        // to be safe, also consider it invalid as a hint if it's > 64998
        match self.st.st_nlink {
            0..=1 => None,
            // # children = minus "." and ".."
            // however, getdents also returns "." and "..", so we don't subtract 2
            2..=64998 => Some(self.st.st_nlink as usize),
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
            for_each_flistxattr(fd, |name| {
                with_fgetxattr(fd, name, |value| {
                    f(name, value)
                })
            })?;
        } else {
            // otherwise, we have to use /proc/self/fd/<dirfd>/<name>
            // flistxattr/fgetxattr doesn't work on O_PATH fds, so the only alternative would be to use a full absolute path, which is slow and requires keeping track of absolute paths when recursing

            with_fd_path(self.dirfd, self.name, |path_cstr| {
                for_each_llistxattr(path_cstr, |name| {
                    with_lgetxattr(path_cstr, name, |value| {
                        f(name, value)
                    })
                })
            })?;
        }

        Ok(())
    }
}

pub fn with_fd_path<T, F: AsRawFd>(dirfd: &F, name: &CStr, f: impl FnOnce(&CStr) -> T) -> T {
    // all of this is a fancy zero-allocation way to do format!("/proc/self/fd/{}/{}\0", dirfd, name)
    let mut fd_buf: [u8; 32] = [0; 32];
    let formatted_fd = dirfd.as_raw_fd().numtoa(10, &mut fd_buf);

    // string formating go brrr
    let mut path_buf: SmallVec<[u8; 1024]> = b"/proc/self/fd/".to_smallvec();
    path_buf.extend_from_slice(formatted_fd);
    path_buf.push(b'/');
    path_buf.extend_from_slice(name.to_bytes_with_nul());

    // with &[u8] instead of &CStr, with_nix_path attempts to add a null terminator
    let path_cstr = unsafe { CStr::from_bytes_with_nul_unchecked(&path_buf) };
    f(path_cstr)
}
