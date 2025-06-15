use crate::endian::{BeU16, BeU32, BeU64};
use bytemuck::{Pod, Zeroable};

pub const NBD_REQUEST_MAGIC: u32 = 0x25609513;

pub const NBD_SIMPLE_REPLY_MAGIC: u32 = 0x67446698;

pub const NBD_CMD_READ: u16 = 0;
pub const NBD_CMD_WRITE: u16 = 1;
pub const NBD_CMD_DISC: u16 = 2;
pub const NBD_CMD_FLUSH: u16 = 3;
pub const NBD_CMD_TRIM: u16 = 4;
pub const NBD_CMD_WRITE_ZEROES: u16 = 6;

pub const NBD_FLAG_HAS_FLAGS: u32 = 1 << 0;
pub const NBD_FLAG_READ_ONLY: u32 = 1 << 1;
pub const NBD_FLAG_SEND_FLUSH: u32 = 1 << 2;
pub const NBD_FLAG_SEND_FUA: u32 = 1 << 3;
pub const NBD_FLAG_ROTATIONAL: u32 = 1 << 4;
pub const NBD_FLAG_SEND_TRIM: u32 = 1 << 5;
pub const NBD_FLAG_SEND_WRITE_ZEROES: u32 = 1 << 6;
pub const NBD_FLAG_CAN_MULTI_CONN: u32 = 1 << 8;

pub const NBD_CMD_FLAG_FUA: u32 = 1 << 16;
pub const NBD_CMD_FLAG_NO_HOLE: u32 = 1 << 17;

#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C, packed)]
pub struct NbdRequest {
    pub magic: BeU32,
    pub command_flags: BeU16,
    pub type_: BeU16,
    pub cookie: BeU64,
    pub offset: BeU64,
    pub length: BeU32,
}

#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C, packed)]
pub struct NbdSimpleReply {
    pub magic: BeU32,
    pub error: BeU32,
    pub cookie: BeU64,
}


