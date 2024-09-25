#[cfg(target_arch = "x86_64")]
mod impl_x86;
#[cfg(target_arch = "x86_64")]
pub use impl_x86::*;
