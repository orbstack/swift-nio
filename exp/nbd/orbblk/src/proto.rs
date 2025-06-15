pub const NBD_REQUEST_MAGIC: u32 = 0x25609513;

pub const NBD_SIMPLE_REPLY_MAGIC: u32 = 0x67446698;

pub const NBD_CMD_READ: u32 = 0;
pub const NBD_CMD_WRITE: u32 = 1;
pub const NBD_CMD_DISC: u32 = 2;
pub const NBD_CMD_FLUSH: u32 = 3;
pub const NBD_CMD_TRIM: u32 = 4;
pub const NBD_CMD_WRITE_ZEROES: u32 = 6;

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

