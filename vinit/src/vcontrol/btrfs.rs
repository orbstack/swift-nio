use nix::libc::__u64;

const BTRFS_IOCTL_MAGIC: u8 = 0x94;
pub const BTRFS_QGROUP_LIMIT_MAX_RFER: u32 = 1;

#[repr(C)]
#[derive(Debug, Copy, Clone)]
pub struct btrfs_qgroup_limit {
    pub flags: __u64,
    pub max_rfer: __u64,
    pub max_excl: __u64,
    pub rsv_rfer: __u64,
    pub rsv_excl: __u64,
}

#[repr(C)]
#[derive(Debug, Copy, Clone)]
pub struct btrfs_ioctl_qgroup_limit_args {
    pub qgroupid: __u64,
    pub lim: btrfs_qgroup_limit,
}

pub mod ioctl {
    use super::*;

    nix::ioctl_read!(qgroup_limit, BTRFS_IOCTL_MAGIC, 43, btrfs_ioctl_qgroup_limit_args);
}
