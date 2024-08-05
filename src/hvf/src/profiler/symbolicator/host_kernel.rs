use super::{SymbolResult, Symbolicator};

// fake symbolicator to inject host kernel ktrace events
pub struct HostKernelSymbolicator {}

impl HostKernelSymbolicator {
    pub const IMAGE: &'static str = "xnu";

    const ADDR_BASE: u64 = 0xffff000000000000;
    pub const ADDR_VMFAULT: u64 = Self::ADDR_BASE + 1;
    pub const ADDR_THREAD_SUSPENDED: u64 = Self::ADDR_BASE + 2;
    pub const ADDR_THREAD_WAIT: u64 = Self::ADDR_BASE + 3;
    pub const ADDR_THREAD_WAIT_UNINTERRUPTIBLE: u64 = Self::ADDR_BASE + 4;
    pub const ADDR_THREAD_HALTED: u64 = Self::ADDR_BASE + 5;

    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {})
    }
}

impl Symbolicator for HostKernelSymbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        Ok(Some(SymbolResult {
            image: Self::IMAGE.to_string(),
            image_base: 0,
            symbol_offset: match addr {
                Self::ADDR_VMFAULT => Some(("MACH_vmfault".to_string(), 0)),
                Self::ADDR_THREAD_SUSPENDED => Some(("thread_suspended".to_string(), 0)),
                Self::ADDR_THREAD_WAIT => Some(("thread_wait".to_string(), 0)),
                Self::ADDR_THREAD_WAIT_UNINTERRUPTIBLE => {
                    Some(("thread_wait_uninterruptible".to_string(), 0))
                }
                Self::ADDR_THREAD_HALTED => Some(("thread_halted".to_string(), 0)),
                _ => None,
            },
        }))
    }
}
