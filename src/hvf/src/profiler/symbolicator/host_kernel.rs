use super::{SymbolResult, Symbolicator};

// fake symbolicator to inject host kernel ktrace events
pub struct HostKernelSymbolicator {}

impl HostKernelSymbolicator {
    pub const IMAGE: &'static str = "xnu";

    const ADDR_BASE: u64 = 0xffff000000000000;
    pub const ADDR_VMFAULT: u64 = Self::ADDR_BASE + 1;

    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {})
    }
}

impl Symbolicator for HostKernelSymbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        Ok(match addr {
            Self::ADDR_VMFAULT => Some(SymbolResult {
                image: Self::IMAGE.to_string(),
                image_base: 0,
                symbol_offset: Some(("MACH_vmfault".to_string(), 0)),
            }),
            _ => None,
        })
    }
}
