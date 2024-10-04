#[cfg(target_arch = "aarch64")]
mod arm64;
#[cfg(target_arch = "aarch64")]
pub use arm64::*;

#[cfg(not(target_arch = "aarch64"))]
pub const MIN_INSN_SIZE: u64 = 1; // byte
