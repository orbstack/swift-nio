use std::path::Path;

use ahash::AHashMap;
use tokio::runtime::Runtime;
use tracing::error;
use wholesym::{
    samply_symbols::object::{
        macho::{MachHeader64, CPU_SUBTYPE_ARM64E},
        read::macho::MachHeader,
        LittleEndian,
    },
    LookupAddress, MultiArchDisambiguator, SymbolManager, SymbolManagerConfig, SymbolMap,
};

use super::{cache::CachedSymbolicator, dladdr::DladdrSymbolicator, SymbolResult, Symbolicator};

// the only advantage of wholesym is that it can use DWARF to get source/line info,
// and inlined frames, but it chokes trying to load arm64 (not arm64e) system libs
// from the dyld shared cache. so use dladdr() as a fallback
pub struct WholesymSymbolicator {
    dladdr: CachedSymbolicator<DladdrSymbolicator>,
    images: AHashMap<String, SymbolMap>,
    manager: SymbolManager,
    rt: Runtime,
}

impl WholesymSymbolicator {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            dladdr: CachedSymbolicator::new(DladdrSymbolicator::new()?),
            images: AHashMap::new(),
            manager: SymbolManager::with_config(SymbolManagerConfig::default()),
            rt: tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()?,
        })
    }
}

impl Symbolicator for WholesymSymbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        let (image_path, image_base) = match self.dladdr.addr_to_symbol(addr)? {
            Some(symbol) => (symbol.image, symbol.image_base),
            None => return Ok(None),
        };

        let image_slice = unsafe { std::slice::from_raw_parts(image_base as *const u8, 16384) };
        let macho = MachHeader64::<LittleEndian>::parse(image_slice, 0)?;

        let res: anyhow::Result<_> = self.rt.block_on(async {
            let sym_map = if let Some(sym_map) = self.images.get(&image_path) {
                sym_map
            } else {
                let sym_map = self
                    .manager
                    .load_symbol_map_for_binary_at_path(
                        Path::new(&image_path),
                        Some(MultiArchDisambiguator::Arch(
                            if macho.cpusubtype(LittleEndian) == CPU_SUBTYPE_ARM64E {
                                "arm64e".to_string()
                            } else {
                                "arm64".to_string()
                            },
                        )),
                    )
                    .await?;
                self.images.insert(image_path.clone(), sym_map);
                self.images.get(&image_path).unwrap()
            };

            let symbol_offset = if let Some(info) = sym_map
                .lookup(LookupAddress::Relative((addr - image_base) as u32))
                .await
            {
                Some((
                    info.symbol.name,
                    (addr - image_base) as usize - info.symbol.address as usize,
                ))
            } else {
                None
            };

            Ok(Some(SymbolResult {
                image: image_path,
                image_base,
                symbol_offset,
            }))
        });

        match res {
            Ok(res) => Ok(res),
            Err(e) => {
                error!(?e, "wholesym failed for address {:x}", addr);
                self.dladdr.addr_to_symbol(addr)
            }
        }
    }
}
