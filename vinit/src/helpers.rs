use std::{fs};

pub const SWAP_FLAG_PREFER: i32 = 0x8000;
pub const SWAP_FLAG_DISCARD: i32 = 0x10000;
pub const SWAP_FLAG_PRIO_MASK: i32 = 0x7fff;
pub const SWAP_FLAG_PRIO_SHIFT: i32 = 0;

pub fn sysctl(key: &str, value: &str) -> std::io::Result<()> {
    let path = format!("/proc/sys/{}", key.replace(".", "/"));
    fs::write(path, value)?;
    Ok(())
}
