#[cfg(target_arch = "x86_64")]
mod hvf_apic;
mod multiplexer;
#[cfg(target_arch = "aarch64")]
mod v3;
#[cfg(target_arch = "aarch64")]
pub use v3::GicSysReg;
#[cfg(target_arch = "aarch64")]
mod hvf_gic;

pub use multiplexer::*;
