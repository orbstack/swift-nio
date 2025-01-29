use nix::libc::__u64;

const BTRFS_IOCTL_MAGIC: u8 = 0x94;
pub const BTRFS_QGROUP_LIMIT_MAX_RFER: u32 = 1;

#[repr(C)]
#[derive(Debug, Copy, Clone)]
pub struct BtrfsQgroupLimit {
    pub flags: __u64,
    pub max_rfer: __u64,
    pub max_excl: __u64,
    pub rsv_rfer: __u64,
    pub rsv_excl: __u64,
}

#[repr(C)]
#[derive(Debug, Copy, Clone)]
pub struct BtrfsIoctlQgroupLimitArgs {
    pub qgroupid: __u64,
    pub lim: BtrfsQgroupLimit,
}

pub mod ioctl {
    use super::*;

    nix::ioctl_read!(
        qgroup_limit,
        BTRFS_IOCTL_MAGIC,
        43,
        BtrfsIoctlQgroupLimitArgs
    );
}

const BTRFS_QGROUP_LEVEL_SHIFT: u64 = 48;

pub const fn make_qgroup_id(level: u64, id: u64) -> u64 {
    level << BTRFS_QGROUP_LEVEL_SHIFT | id
}
