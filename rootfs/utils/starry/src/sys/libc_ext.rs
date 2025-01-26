// copied from libc crate, because it only declares these for glibc and not musl
#![allow(overflowing_literals)]
#![allow(non_camel_case_types)]
#![allow(non_upper_case_globals)]
#![allow(clippy::missing_safety_doc)]

use libc::{c_char, c_int, c_uint};

pub const AT_STATX_SYNC_TYPE: c_int = 0x6000;
pub const AT_STATX_SYNC_AS_STAT: c_int = 0x0000;
pub const AT_STATX_FORCE_SYNC: c_int = 0x2000;
pub const AT_STATX_DONT_SYNC: c_int = 0x4000;
pub const STATX_TYPE: c_uint = 0x0001;
pub const STATX_MODE: c_uint = 0x0002;
pub const STATX_NLINK: c_uint = 0x0004;
pub const STATX_UID: c_uint = 0x0008;
pub const STATX_GID: c_uint = 0x0010;
pub const STATX_ATIME: c_uint = 0x0020;
pub const STATX_MTIME: c_uint = 0x0040;
pub const STATX_CTIME: c_uint = 0x0080;
pub const STATX_INO: c_uint = 0x0100;
pub const STATX_SIZE: c_uint = 0x0200;
pub const STATX_BLOCKS: c_uint = 0x0400;
pub const STATX_BASIC_STATS: c_uint = 0x07ff;
pub const STATX_BTIME: c_uint = 0x0800;
pub const STATX_ALL: c_uint = 0x0fff;
pub const STATX_MNT_ID: c_uint = 0x1000;
pub const STATX_DIOALIGN: c_uint = 0x2000;
pub const STATX__RESERVED: c_int = 0x80000000;
pub const STATX_ATTR_COMPRESSED: c_int = 0x0004;
pub const STATX_ATTR_IMMUTABLE: c_int = 0x0010;
pub const STATX_ATTR_APPEND: c_int = 0x0020;
pub const STATX_ATTR_NODUMP: c_int = 0x0040;
pub const STATX_ATTR_ENCRYPTED: c_int = 0x0800;
pub const STATX_ATTR_AUTOMOUNT: c_int = 0x1000;
pub const STATX_ATTR_MOUNT_ROOT: c_int = 0x2000;
pub const STATX_ATTR_VERITY: c_int = 0x100000;
pub const STATX_ATTR_DAX: c_int = 0x200000;

#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct statx {
    pub stx_mask: libc::__u32,
    pub stx_blksize: libc::__u32,
    pub stx_attributes: libc::__u64,
    pub stx_nlink: libc::__u32,
    pub stx_uid: libc::__u32,
    pub stx_gid: libc::__u32,
    pub stx_mode: libc::__u16,
    __statx_pad1: [libc::__u16; 1],
    pub stx_ino: libc::__u64,
    pub stx_size: libc::__u64,
    pub stx_blocks: libc::__u64,
    pub stx_attributes_mask: libc::__u64,
    pub stx_atime: statx_timestamp,
    pub stx_btime: statx_timestamp,
    pub stx_ctime: statx_timestamp,
    pub stx_mtime: statx_timestamp,
    pub stx_rdev_major: libc::__u32,
    pub stx_rdev_minor: libc::__u32,
    pub stx_dev_major: libc::__u32,
    pub stx_dev_minor: libc::__u32,
    pub stx_mnt_id: libc::__u64,
    pub stx_dio_mem_align: libc::__u32,
    pub stx_dio_offset_align: libc::__u32,
    __statx_pad3: [libc::__u64; 12],
}

#[repr(C)]
#[derive(Debug, Clone, Copy)]
pub struct statx_timestamp {
    pub tv_sec: libc::__s64,
    pub tv_nsec: libc::__u32,
    __statx_timestamp_pad1: [libc::__s32; 1],
}

// musl has no wrapper for this
pub unsafe fn close_range(fd: c_uint, max_fd: c_uint, flags: c_int) -> c_int {
    libc::syscall(libc::SYS_close_range, fd, max_fd, flags) as c_int
}

// musl's wrapper for this is very new
pub unsafe fn statx(
    dirfd: c_int,
    pathname: *const c_char,
    flags: c_int,
    mask: c_uint,
    statxbuf: *mut statx,
) -> c_int {
    libc::syscall(libc::SYS_statx, dirfd, pathname, flags, mask, statxbuf) as c_int
}
