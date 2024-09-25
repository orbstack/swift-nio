use std::path::Path;

use addr2line::{fallible_iterator::FallibleIterator, Loader};
use anyhow::anyhow;
use smallvec::smallvec;
use tracing::error;
use utils::kernel_symbols::CompactSystemMap;

use super::{SymbolFunc, SymbolResult, SymbolResults, Symbolicator};

pub struct LinuxSymbolicator {
    dwarf: Option<Loader>,
    csmap: CompactSystemMap,
    kaslr_offset: i64,
    image_base: u64,
}

impl LinuxSymbolicator {
    pub fn new(
        csmap: CompactSystemMap,
        csmap_path: &str,
        kaslr_offset: i64,
    ) -> anyhow::Result<Self> {
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

        // DWARF is in "kernel.vmlinux", next to csmap, if it exists
        let dwarf = Loader::new(
            Path::new(csmap_path)
                .parent()
                .unwrap_or(Path::new("/"))
                .join("kernel.vmlinux"),
        )
        .inspect_err(|e| error!("failed to load kernel DWARF: {}", e))
        .ok();

        Ok(Self {
            dwarf,
            csmap,
            kaslr_offset,
            image_base,
        })
    }
}

impl Symbolicator for LinuxSymbolicator {
    fn addr_to_symbols(&mut self, addr: u64) -> anyhow::Result<SymbolResults> {
        // subtract KASLR offset to get vaddr in System.map
        let vaddr = addr.checked_add_signed(self.kaslr_offset).ok_or_else(|| {
            anyhow!(
                "overflow: kaslr_offset={:#x} addr={:#x}",
                self.kaslr_offset,
                addr
            )
        })?;

        // lookup symbol in DWARF
        if let Some(ref mut dwarf) = self.dwarf {
            if let Ok(frames) = dwarf.find_frames(vaddr) {
                return Ok(frames
                    .map(|frame| {
                        Ok(SymbolResult {
                            image: "linux".to_string(),
                            image_base: self.image_base,
                            function: frame.function.map(|name| {
                                SymbolFunc::Function(name.raw_name().unwrap().to_string(), 0)
                            }),
                            source: frame.location.and_then(|loc| {
                                loc.file
                                    .map(|file| (file.to_string(), loc.line.unwrap_or(0)))
                            }),
                        })
                    })
                    .collect()?);
            }
        }

        // lookup symbol in System.map
        Ok(self
            .csmap
            .vaddr_to_symbol(vaddr)
            .map(|(symbol, offset)| {
                smallvec![SymbolResult {
                    image: "linux".to_string(),
                    image_base: self.image_base,
                    function: Some(SymbolFunc::Function(symbol.to_string(), offset)),
                    source: None,
                }]
            })
            .unwrap_or_else(|| smallvec![]))
    }
}
