use anyhow::anyhow;
use utils::kernel_symbols::CompactSystemMap;

use super::{SymbolResult, Symbolicator};

#[derive(Clone)]
pub struct LinuxSymbolicator {
    csmap: CompactSystemMap,
    kaslr_offset: i64,
    image_base: u64,
}

impl LinuxSymbolicator {
    pub fn new(csmap: CompactSystemMap, kaslr_offset: i64) -> anyhow::Result<Self> {
        // using kaslr offset and _text, we can calculate the base:
        let base = csmap
            .symbol_to_vaddr("_text")
            .ok_or_else(|| anyhow!("no _text symbol"))?;
        // kaslr_offset is signed in the direction of KASLR -> ELF
        // so negate it to get ELF -> KASLR
        let image_base = base.checked_add_signed(-kaslr_offset).ok_or_else(|| {
            anyhow!(
                "overflow: base={:#x} kaslr_offset={:#x}",
                base,
                kaslr_offset
            )
        })?;

        Ok(Self {
            csmap,
            kaslr_offset,
            image_base,
        })
    }
}

impl Symbolicator for LinuxSymbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        // subtract KASLR offset to get vaddr in System.map
        let vaddr = addr.checked_add_signed(self.kaslr_offset).ok_or_else(|| {
            anyhow!(
                "overflow: kaslr_offset={:#x} addr={:#x}",
                self.kaslr_offset,
                addr
            )
        })?;

        // lookup symbol in System.map
        Ok(self
            .csmap
            .vaddr_to_symbol(vaddr)
            .map(|(symbol, offset)| SymbolResult {
                image: "linux".to_string(),
                image_base: self.image_base,
                symbol_offset: Some((symbol.to_string(), offset)),
            }))
    }
}
