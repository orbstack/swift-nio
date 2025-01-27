use std::{cell::RefCell, io::Write};

use bincode::{Decode, Encode};

thread_local! {
    static ENCODE_BUF: RefCell<Vec<u8>> = const { RefCell::new(Vec::new()) };
}

#[derive(Debug, Clone, Default, Encode, Decode)]
pub enum EntryType {
    #[default]
    Regular,
    RegularSparse,
    Directory,
    Symlink,
    Hardlink,
    Char,
    Block,
    Fifo,
    Socket,
}

#[derive(Debug, Clone, Default, Encode, Decode)]
pub struct FileFlags(pub u16);

impl FileFlags {
    pub const INODE_IMMUTABLE: u16 = 1 << 0;
    pub const INODE_APPEND_ONLY: u16 = 1 << 1;
    pub const INODE_NODUMP: u16 = 1 << 2;
}

#[derive(Debug, Clone, Default, Encode, Decode)]
pub struct EntryHeader {
    pub data_size: u64,
    pub file_type: EntryType,
    pub mode: u16,
    pub uid: u32,
    pub gid: u32,

    pub flags: FileFlags,

    pub atime: TimeSpec,
    pub mtime: TimeSpec,

    pub path: Vec<u8>,
    pub xattrs: Vec<(Vec<u8>, Vec<u8>)>,
}

impl EntryHeader {
    pub fn write_to(&self, writer: &mut impl Write) -> anyhow::Result<()> {
        // TODO
        let buf = bincode::encode_to_vec(self, bincode::config::standard())?;
        writer.write_all(&buf)?;
        Ok(())
    }
}

#[derive(Debug, Clone, Default, Encode, Decode)]
pub struct TimeSpec {
    pub seconds: i64,
    pub nanoseconds: u32,
}

impl TimeSpec {
    pub fn new(seconds: i64, nanoseconds: u32) -> Self {
        Self {
            seconds,
            nanoseconds,
        }
    }
}

#[derive(Debug, Clone, Encode, Decode)]
pub struct SparseHeader {
    pub offset: u64,
    pub length: u64,
}

impl SparseHeader {
    pub fn write_to(&self, writer: &mut impl Write) -> anyhow::Result<()> {
        let buf = bincode::encode_to_vec(self, bincode::config::standard())?;
        writer.write_all(&buf)?;
        Ok(())
    }
}

#[derive(Debug, Clone, Encode, Decode)]
pub struct DeviceInfo {
    pub major: u32,
    pub minor: u32,
}

impl DeviceInfo {
    pub fn write_to(&self, writer: &mut impl Write) -> anyhow::Result<()> {
        let buf = bincode::encode_to_vec(self, bincode::config::standard())?;
        writer.write_all(&buf)?;
        Ok(())
    }
}
