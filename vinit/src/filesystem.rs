use std::{fs::File, os::unix::fs::FileExt};

const BCACHEFS_OFFSET: u64 = 0xe00;
const BCACHEFS_MAGIC: &[u8] = b"\xc6\x85\x73\xf6\x66\xce\x90\xa9\xd9\x6a\x60\xcf\x80\x3d\xf7\xef";

pub enum FsType {
    Btrfs,
    Bcachefs,
}

impl FsType {
    pub fn detect(dev_path: &str) -> anyhow::Result<FsType> {
        let file = File::open(dev_path)?;
        let mut magic = [0; 16];
        file.read_exact_at(&mut magic, BCACHEFS_OFFSET)?;

        if magic == BCACHEFS_MAGIC {
            Ok(FsType::Bcachefs)
        } else {
            Ok(FsType::Btrfs)
        }
    }
}
