use std::ops::Range;

use anyhow::anyhow;

#[derive(Debug, Clone)]
pub struct SymbolResult {
    pub image: String,
    pub image_base: u64,
    pub function: Option<SymbolFunc>,
    pub source: Option<(String, u32)>,
}

#[derive(Debug, Clone)]
pub enum SymbolFunc {
    Function(String, usize),
    Inlined(String),
}

impl SymbolFunc {
    pub fn name(&self) -> &str {
        match self {
            SymbolFunc::Function(name, _) => name,
            SymbolFunc::Inlined(name) => name,
        }
    }

    pub fn name_offset(&self) -> (&str, usize) {
        match self {
            SymbolFunc::Function(name, offset) => (name, *offset),
            SymbolFunc::Inlined(name) => (name, 0),
        }
    }
}

pub type SymbolResults = SmallVec<[SymbolResult; 2]>;

pub trait Symbolicator {
    fn addr_to_symbols(&mut self, addr: u64) -> anyhow::Result<SymbolResults>;

    fn symbol_range(&mut self, _name: &str) -> anyhow::Result<Option<Range<usize>>> {
        Err(anyhow!("not implemented"))
    }
}

mod cache;
mod dladdr;
mod dwarf;
mod host_kernel;
mod linux;
#[cfg(feature = "profiler-wholesym")]
mod wholesym;

pub use cache::CachedSymbolicator;
pub use dladdr::DladdrSymbolicator;
pub use dwarf::HostDwarfSymbolicator;
pub use host_kernel::HostKernelSymbolicator;
pub use linux::LinuxSymbolicator;
use smallvec::SmallVec;
#[cfg(feature = "profiler-wholesym")]
pub use wholesym::WholesymSymbolicator;
