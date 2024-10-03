use std::os::fd::AsRawFd;

use bitflags::bitflags;
use nix::errno::Errno;

bitflags! {
    #[repr(transparent)]
    #[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Hash)]
    pub struct InodeFlags: u32 {
        // FS_<FLAG>_FL
        const SECRM = 0x00000001;
        const UNRM = 0x00000002;
        const COMPR = 0x00000004;
        const SYNC = 0x00000008;
        const IMMUTABLE = 0x00000010;
        const APPEND = 0x00000020;
        const NODUMP = 0x00000040;
        const NOATIME = 0x00000080;
        const DIRTY = 0x00000100;
        const COMPRBLK = 0x00000200;
        const NOCOMP = 0x00000400;
        const ENCRYPT = 0x00000800;
        //const BTREE = 0x00001000;
        const INDEX = 0x00001000;
        const IMAGIC = 0x00002000;
        const JOURNAL_DATA = 0x00004000;
        const NOTAIL = 0x00008000;
        const DIRSYNC = 0x00010000;
        const TOPDIR = 0x00020000;
        const HUGE_FILE = 0x00040000;
        const EXTENT = 0x00080000;
        const VERITY = 0x00100000;
        const EA_INODE = 0x00200000;
        const EOFBLOCKS = 0x00400000;
        const NOCOW = 0x00800000;
        const DAX = 0x02000000;
        const INLINE_DATA = 0x10000000;
        const PROJINHERIT = 0x20000000;
        const CASEFOLD = 0x40000000;
        const RESERVED = 0x80000000;
    }
}

impl InodeFlags {
    pub fn from_file<F: AsRawFd>(fd: &F) -> nix::Result<Self> {
        let mut flags = Self::empty();
        let ret = unsafe { libc::ioctl(fd.as_raw_fd(), libc::FS_IOC_GETFLAGS, &mut flags) };
        Errno::result(ret)?;
        Ok(flags)
    }
}
