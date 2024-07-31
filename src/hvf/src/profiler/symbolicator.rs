use std::{
    cell::RefCell,
    collections::HashMap,
    ffi::{c_void, CStr, CString},
    mem::MaybeUninit,
    ops::Range,
    path::Path,
};

use anyhow::anyhow;
use libc::{dladdr, dlsym, Dl_info, RTLD_NEXT};
use tokio::runtime::Runtime;
use utils::kernel_symbols::CompactSystemMap;
use wholesym::{
    LookupAddress, MultiArchDisambiguator, SymbolManager, SymbolManagerConfig, SymbolMap,
};

#[derive(Debug, Clone)]
pub struct SymbolResult {
    pub image: String,
    pub image_base: u64,
    pub symbol_offset: Option<(String, usize)>,
}

pub trait Symbolicator {
    fn addr_to_symbol(&self, addr: u64) -> anyhow::Result<Option<SymbolResult>>;

    fn symbol_range(&self, _name: &str) -> anyhow::Result<Option<Range<usize>>> {
        Err(anyhow!("not implemented"))
    }
}

pub struct MacSymbolicator {}

impl Symbolicator for MacSymbolicator {
    fn addr_to_symbol(&self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        let mut info = MaybeUninit::<Dl_info>::uninit();
        let ret = unsafe { dladdr(addr as *const c_void, info.as_mut_ptr()) };
        if ret == 0 {
            tracing::error!("dladdr failed for address {:x}", addr);
            return Ok(None);
        }
        let info = unsafe { info.assume_init() };

        let image = unsafe { CStr::from_ptr(info.dli_fname) }
            .to_string_lossy()
            .to_string();
        let image_base = info.dli_fbase as u64;

        let symbol_offset = if !info.dli_sname.is_null() {
            let symbol = unsafe { CStr::from_ptr(info.dli_sname) }.to_string_lossy();
            let offset = (addr - info.dli_saddr as u64) as usize;
            let demangled = symbolic_demangle::demangle(&symbol);
            Some((demangled.to_string(), offset))
        } else {
            None
        };

        Ok(Some(SymbolResult {
            image,
            image_base,
            symbol_offset,
        }))
    }

    fn symbol_range(&self, name: &str) -> anyhow::Result<Option<Range<usize>>> {
        // find symbol start addr
        let name_c = CString::new(name)?;
        let start_addr = unsafe { dlsym(RTLD_NEXT, name_c.as_ptr()) };
        if start_addr.is_null() {
            return Ok(None);
        }

        // scan until this symbol ends
        let mut p = start_addr as *const u8;
        loop {
            let mut info = MaybeUninit::<Dl_info>::uninit();
            let ret = unsafe { dladdr(p as *const _, info.as_mut_ptr()) };
            if ret == 0 {
                // no longer in any image
                break;
            }
            let info = unsafe { info.assume_init() };

            if info.dli_sname.is_null() {
                // no symbol here
                break;
            }

            let cur_symbol = unsafe { CStr::from_ptr(info.dli_sname) }.to_string_lossy();
            if cur_symbol != name {
                // no longer in the symbol we're looking for
                break;
            }

            p = unsafe { p.add(4) };
        }

        Ok(Some(start_addr as usize..p as usize))
    }
}

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
        let image_base = base.wrapping_add_signed(-kaslr_offset);

        Ok(Self {
            csmap,
            kaslr_offset,
            image_base,
        })
    }
}

impl Symbolicator for LinuxSymbolicator {
    fn addr_to_symbol(&self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        // subtract KASLR offset to get vaddr in System.map
        let vaddr = addr.wrapping_add_signed(self.kaslr_offset);

        // lookup symbol in System.map
        Ok(self
            .csmap
            .vaddr_to_symbol(vaddr)
            .map(|(symbol, offset)| SymbolResult {
                image: "kernel".to_string(),
                image_base: self.image_base,
                symbol_offset: Some((symbol.to_string(), offset)),
            }))
    }
}

pub struct WholesymSymbolicator {
    dladdr: CachedSymbolicator<MacSymbolicator>,
    images: RefCell<HashMap<String, SymbolMap>>,
    manager: SymbolManager,
    rt: Runtime,
}

impl WholesymSymbolicator {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            dladdr: CachedSymbolicator::new(MacSymbolicator {}),
            images: RefCell::new(HashMap::new()),
            manager: SymbolManager::with_config(SymbolManagerConfig::default()),
            rt: tokio::runtime::Builder::new_current_thread()
                .enable_all()
                .build()?,
        })
    }
}

impl Symbolicator for WholesymSymbolicator {
    fn addr_to_symbol(&self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        let (image_path, image_base) = match self.dladdr.addr_to_symbol(addr)? {
            Some(symbol) => (symbol.image, symbol.image_base),
            None => return Ok(None),
        };

        self.rt.block_on(async move {
            let mut images = self.images.borrow_mut();
            let sym_map = if let Some(sym_map) = images.get(&image_path) {
                sym_map
            } else {
                let sym_map = self
                    .manager
                    .load_symbol_map_for_binary_at_path(
                        Path::new(&image_path),
                        Some(MultiArchDisambiguator::BestMatchForNative),
                    )
                    .await?;
                images.insert(image_path.clone(), sym_map);
                images.get(&image_path).unwrap()
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
        })
    }
}

pub struct CachedSymbolicator<S> {
    cache: RefCell<HashMap<u64, Option<SymbolResult>>>,
    inner: S,
}

impl<S: Symbolicator> CachedSymbolicator<S> {
    pub fn new(inner: S) -> Self {
        Self {
            cache: RefCell::new(HashMap::new()),
            inner,
        }
    }
}

impl<S: Symbolicator> Symbolicator for CachedSymbolicator<S> {
    fn addr_to_symbol(&self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        let mut cache = self.cache.borrow_mut();
        if let Some(symbol) = cache.get(&addr) {
            return Ok(symbol.clone());
        }

        let symbol = self.inner.addr_to_symbol(addr)?;
        cache.insert(addr, symbol.clone());
        Ok(symbol)
    }
}
