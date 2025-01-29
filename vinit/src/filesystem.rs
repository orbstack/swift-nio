use std::{
    fs::File,
    os::{fd::AsRawFd, unix::fs::FileExt},
};

use nix::{errno::Errno, sys::statvfs::statvfs};
use serde::{Deserialize, Serialize};

use crate::btrfs;

const BCACHEFS_OFFSET: u64 = 0xe00;
const BCACHEFS_MAGIC: &[u8] = b"\xc6\x85\x73\xf6\x66\xce\x90\xa9\xd9\x6a\x60\xcf\x80\x3d\xf7\xef";

const XFS_OFFSET: u64 = 0;
const XFS_MAGIC: &[u8] = b"XFSB";

const EXT4_OFFSET: u64 = 0x0438;
const EXT4_MAGIC: &[u8] = b"\x53\xef";

const F2FS_OFFSET: u64 = 0x400;
const F2FS_MAGIC: &[u8] = b"\x10\x20\xF5\xF2";

pub enum FsType {
    Btrfs,
    Bcachefs,
    Xfs,
    Ext4,
    F2fs,
}

impl FsType {
    pub fn detect(dev_path: &str) -> anyhow::Result<FsType> {
        let file = File::open(dev_path)?;

        if Self::check_magic(&file, BCACHEFS_OFFSET, BCACHEFS_MAGIC)? {
            return Ok(FsType::Bcachefs);
        }

        if Self::check_magic(&file, XFS_OFFSET, XFS_MAGIC)? {
            return Ok(FsType::Xfs);
        }

        if Self::check_magic(&file, EXT4_OFFSET, EXT4_MAGIC)? {
            return Ok(FsType::Ext4);
        }

        if Self::check_magic(&file, F2FS_OFFSET, F2FS_MAGIC)? {
            return Ok(FsType::F2fs);
        }

        Ok(FsType::Btrfs)
    }

    fn check_magic(file: &File, offset: u64, magic: &[u8]) -> anyhow::Result<bool> {
        let mut buf = [0; 16];
        file.read_exact_at(&mut buf[..magic.len()], offset)?;
        Ok(&buf[..magic.len()] == magic)
    }
}

#[derive(Serialize, Deserialize, Clone, Debug)]
#[serde(rename_all = "camelCase")]
pub struct HostDiskStats {
    pub host_fs_free: u64,
    pub data_img_size: u64,
}

// btrfs doesn't really have this much overhead
const BASE_FS_OVERHEAD: u64 = 100 * 1024 * 1024; // 100MiB
                                                 // can't use more than 95% of the host's free space
const MAX_HOST_FS_PERCENT: u64 = 95;
// can't boot without free space for scon db. leave some - I/O error + R/O remount is better than no boot
const MIN_FREE_SPACE: u64 = 2 * 1024 * 1024; // 2 MiB

const QGROUP_GLOBAL: u64 = btrfs::make_qgroup_id(1, 1);

#[derive(Debug)]
pub struct DiskManager {}

impl DiskManager {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {})
    }

    fn update_quota(&self, new_size: u64) -> anyhow::Result<()> {
        let dir_file = File::open("/data")?;

        // first attempt to set quota on the new global 1/1 qgroup
        let mut args = btrfs::BtrfsIoctlQgroupLimitArgs {
            qgroupid: QGROUP_GLOBAL,
            lim: btrfs::BtrfsQgroupLimit {
                flags: btrfs::BTRFS_QGROUP_LIMIT_MAX_RFER as u64,
                max_rfer: new_size,
                max_excl: 0,
                rsv_rfer: 0,
                rsv_excl: 0,
            },
        };
        let res = unsafe { btrfs::ioctl::qgroup_limit(dir_file.as_raw_fd(), &mut args) };
        match res {
            Ok(_) => return Ok(()),
            // fallthrough: attempt on legacy 0/5 root subvolume qgroup
            Err(Errno::ENOENT) => {}
            Err(e) => return Err(e.into()),
        }

        // try again on legacy 0/5 root subvolume qgroup
        args.qgroupid = 0;
        unsafe {
            btrfs::ioctl::qgroup_limit(dir_file.as_raw_fd(), &mut args)?;
        }

        Ok(())
    }

    pub fn update_with_stats(&self, stats: &HostDiskStats) -> anyhow::Result<()> {
        let guest_statfs = statvfs("/data")?;

        // (blocks - free) = df
        // (blocks - avail) = matches qgroup rfer, when we have quota statfs
        let guest_fs_size = guest_statfs.blocks() * guest_statfs.block_size();
        let guest_free = guest_statfs.blocks_available() * guest_statfs.block_size();

        // Total free space for data img on host
        // = 95% of free space, plus existing data img size
        // can't take 95% of the sum, because it's possible that data img size > free space
        let max_fs_size = (stats.host_fs_free * MAX_HOST_FS_PERCENT / 100) + stats.data_img_size;
        // Subtract FS overhead - we're setting FS quota, not disk img size limit
        // so FS limit should be a bit lower than the disk img
        let max_data_size = max_fs_size - BASE_FS_OVERHEAD;

        // For quota, just use that size.

        // Don't limit it more than currently used (according to qgroup)
        let guest_used = guest_fs_size - guest_free;
        // prevent ENOSPC boot failure by always leaving a bit of free space
        let max_data_size = max_data_size.max(guest_used) + MIN_FREE_SPACE;

        //info!("guest_fs_size={} guest_free={} total_host_free={} max_fs_size={} max_data_size={} guest_used={}", guest_fs_size, guest_free, total_host_free, max_fs_size, max_data_size, guest_used);
        self.update_quota(max_data_size)?;

        Ok(())
    }
}
