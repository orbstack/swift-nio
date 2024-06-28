use std::{fs::File, os::unix::fs::FileExt};

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

fn check_magic(file: &File, offset: u64, magic: &[u8]) -> anyhow::Result<bool> {
    let mut buf = [0; 16];
    file.read_exact_at(&mut buf[..magic.len()], offset)?;
    Ok(&buf[..magic.len()] == magic)
}

impl FsType {
    pub fn detect(dev_path: &str) -> anyhow::Result<FsType> {
        let file = File::open(dev_path)?;

        if check_magic(&file, BCACHEFS_OFFSET, BCACHEFS_MAGIC)? {
            return Ok(FsType::Bcachefs);
        }

        if check_magic(&file, XFS_OFFSET, XFS_MAGIC)? {
            return Ok(FsType::Xfs);
        }

        if check_magic(&file, EXT4_OFFSET, EXT4_MAGIC)? {
            return Ok(FsType::Ext4);
        }

        if check_magic(&file, F2FS_OFFSET, F2FS_MAGIC)? {
            return Ok(FsType::F2fs);
        }

        Ok(FsType::Btrfs)
    }
}
