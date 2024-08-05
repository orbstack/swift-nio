use std::ops::Range;

use anyhow::anyhow;

#[derive(Debug, Clone)]
pub struct SymbolResult {
    pub image: String,
    pub image_base: u64,
    pub symbol_offset: Option<(String, usize)>,
}

pub trait Symbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>>;

    fn symbol_range(&mut self, _name: &str) -> anyhow::Result<Option<Range<usize>>> {
        Err(anyhow!("not implemented"))
    }
}

mod cache;
mod dladdr;
mod host_kernel;
mod linux;
#[cfg(feature = "profiler-wholesym")]
mod wholesym;

pub use cache::CachedSymbolicator;
pub use dladdr::DladdrSymbolicator;
pub use host_kernel::HostKernelSymbolicator;
pub use linux::LinuxSymbolicator;
#[cfg(feature = "profiler-wholesym")]
pub use wholesym::WholesymSymbolicator;
