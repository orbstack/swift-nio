use std::{
    borrow::Cow,
    ffi::CString,
    mem::size_of,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
};

use libc::{
    syscall, SYS_fsconfig, SYS_fsmount, SYS_fsopen, SYS_fspick, SYS_mount_setattr, SYS_move_mount, SYS_open_tree, SYS_umount2, AT_FDCWD
};

use crate::err;

pub const FSOPEN_CLOEXEC: u32 = 1;
pub const FSMOUNT_CLOEXEC: u32 = 1;

pub const FSCONFIG_SET_FLAG: u32 = 0;
pub const FSCONFIG_SET_STRING: u32 = 1;
pub const FSCONFIG_CMD_CREATE: u32 = 6;
pub const FSCONFIG_CMD_RECONFIGURE: u32 = 7;

pub const FSPICK_CLOEXEC: u32 = 1;
pub const FSPICK_SYMLINK_NOFOLLOW: u32 = 2;
pub const FSPICK_NO_AUTOMOUNT: u32 = 4;
pub const FSPICK_EMPTY_PATH: u32 = 8;

pub const MOUNT_ATTR_RDONLY: u64 = 1;

// musl is missing this
const MOVE_MOUNT_F_EMPTY_PATH: libc::c_uint = 0x00000004;
const MOVE_MOUNT_T_EMPTY_PATH: libc::c_uint = 0x00000040;

#[repr(C)]
#[derive(Debug, Default, Clone, Copy)]
pub struct MountAttr {
    pub attr_set: u64,
    pub attr_clr: u64,
    pub propagation: u64,
    pub userns_fd: u64,
}

pub fn open_tree(path: &str, flags: i32) -> anyhow::Result<OwnedFd> {
    let path = CString::new(path)?;
    let fd = unsafe { err(syscall(SYS_open_tree, AT_FDCWD, path.into_raw(), flags))? };
    Ok(unsafe { OwnedFd::from_raw_fd(fd as i32) })
}

pub fn fsopen(fstype: &str, flags: u32) -> anyhow::Result<OwnedFd> {
    let sb_fd = unsafe { err(syscall(SYS_fsopen, CString::new(fstype)?.as_ptr(), flags))? };
    Ok(unsafe { OwnedFd::from_raw_fd(sb_fd as i32) })
}

pub fn fspick(dirfd: &OwnedFd, path: &str, flags: u32) -> anyhow::Result<OwnedFd> {
    let path = CString::new(path)?;
    let fd = unsafe {
        err(syscall(
            SYS_fspick,
            dirfd.as_raw_fd(),
            path.into_raw(),
            flags,
        ))?
    };
    Ok(unsafe { OwnedFd::from_raw_fd(fd as i32) })
}

pub fn fsconfig(
    sb_fd: &OwnedFd,
    cmd: u32,
    key: Option<&str>,
    value: Option<&str>,
    flags: u32,
) -> anyhow::Result<()> {
    let key = key.map(|s| CString::new(s).unwrap());
    let value = value.map(|s| CString::new(s).unwrap());
    unsafe {
        err(syscall(
            SYS_fsconfig,
            sb_fd.as_raw_fd(),
            cmd,
            key.as_ref().map(|s| s.as_ptr()).unwrap_or(std::ptr::null()),
            value
                .as_ref()
                .map(|s| s.as_ptr())
                .unwrap_or(std::ptr::null()),
            flags,
        ))?
    };
    Ok(())
}

pub fn fsmount(sb_fd: &OwnedFd, flags: u32, attrs: u32) -> anyhow::Result<OwnedFd> {
    let fd = unsafe { err(syscall(SYS_fsmount, sb_fd.as_raw_fd(), flags, attrs))? };
    Ok(unsafe { OwnedFd::from_raw_fd(fd as i32) })
}

pub fn move_mount(
    src_fd: Option<&OwnedFd>,
    src_path: Option<&str>,
    dest_fd: Option<&OwnedFd>,
    dest_path: Option<&str>,
) -> anyhow::Result<()> {
    let mut flags = 0;
    let dest_path = match dest_path {
        Some(d) => Cow::Owned(CString::new(d).unwrap()),
        None => {
            flags |= MOVE_MOUNT_T_EMPTY_PATH;
            Cow::Borrowed(c"")
        }
    };
    let src_path = match src_path {
        Some(s) => Cow::Owned(CString::new(s).unwrap()),
        None => {
            flags |= MOVE_MOUNT_F_EMPTY_PATH;
            Cow::Borrowed(c"")
        }
    };

    unsafe {
        err(syscall(
            SYS_move_mount,
            src_fd.map(|d| d.as_raw_fd()).unwrap_or(AT_FDCWD),
            src_path.as_ptr(),
            dest_fd.map(|d| d.as_raw_fd()).unwrap_or(AT_FDCWD),
            dest_path.as_ptr(),
            flags,
        ))?
    };

    Ok(())
}

pub fn mount_setattr(
    dirfd: Option<&OwnedFd>,
    path: &str,
    flags: u32,
    attr: &MountAttr,
) -> anyhow::Result<()> {
    let path = CString::new(path)?;
    let attr = attr as *const MountAttr;
    unsafe {
        err(syscall(
            SYS_mount_setattr,
            dirfd.map(|d| d.as_raw_fd()).unwrap_or(AT_FDCWD),
            path.into_raw(),
            flags,
            attr,
            size_of::<MountAttr>(),
        ))?
    };
    Ok(())
}
