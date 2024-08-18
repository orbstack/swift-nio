use std::collections::BTreeMap;

use addr2line::{fallible_iterator::FallibleIterator, Loader};
use anyhow::anyhow;
use tracing::error;

use crate::profiler::dyld::{self, LoadedImage};

use super::{
    CachedSymbolicator, DladdrSymbolicator, SymbolFunc, SymbolResult, SymbolResults, Symbolicator,
};

struct DwarfImage {
    image: LoadedImage,
    loader: Loader,
}

pub struct HostDwarfSymbolicator {
    dladdr: CachedSymbolicator<DladdrSymbolicator>,
    images: BTreeMap<usize, DwarfImage>,
}

impl HostDwarfSymbolicator {
    pub fn new() -> anyhow::Result<Self> {
        let mut images = BTreeMap::new();
        for img in dyld::get_loaded_images()? {
            match Loader::new(&img.path) {
                Ok(loader) => {
                    images.insert(img.addr_range.start, DwarfImage { image: img, loader });
                }
                Err(e) => {
                    if e.downcast_ref::<std::io::Error>().map(|e| e.kind())
                        == Some(std::io::ErrorKind::NotFound)
                    {
                        // ignore missing DWARF and use dladdr
                        continue;
                    }

                    error!(?e, path = img.path, "failed to load DWARF");
                }
            }
        }

        Ok(HostDwarfSymbolicator {
            // fallback
            dladdr: CachedSymbolicator::new(DladdrSymbolicator::new()?),
            images,
        })
    }

    fn image_for_addr(&self, addr: u64) -> Option<&DwarfImage> {
        self.images
            .range(..=addr as usize)
            .next_back()
            .and_then(|(_, img)| {
                if img.image.addr_range.contains(&(addr as usize)) {
                    Some(img)
                } else {
                    None
                }
            })
    }
}

impl Symbolicator for HostDwarfSymbolicator {
    fn addr_to_symbols(&mut self, addr: u64) -> anyhow::Result<SymbolResults> {
        let Some(img) = self.image_for_addr(addr) else {
            return self.dladdr.addr_to_symbols(addr);
        };

        // undo ASLR slide
        let vaddr = addr
            .checked_add_signed(-img.image.vmaddr_slide as i64)
            .ok_or_else(|| {
                anyhow!(
                    "overflow when unsliding address: {:#x} - {:#x}",
                    addr,
                    img.image.vmaddr_slide,
                )
            })?;

        Ok(img
            .loader
            .find_frames(vaddr)
            .map_err(|e| anyhow!("failed to find frames: {}", e))?
            .map(|frame| {
                Ok(SymbolResult {
                    image: img.image.path.clone(),
                    image_base: img.image.addr_range.start as u64,
                    function: frame.function.map(|name| {
                        SymbolFunc::Function(
                            symbolic_demangle::demangle(&name.raw_name().unwrap()).to_string(),
                            0,
                        )
                    }),
                    source: frame.location.and_then(|loc| {
                        loc.file
                            .map(|file| (file.to_string(), loc.line.unwrap_or(0)))
                    }),
                })
            })
            .collect()?)
    }
}
