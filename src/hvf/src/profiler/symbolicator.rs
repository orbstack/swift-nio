use std::{
    ffi::{c_void, CStr, CString},
    marker::PhantomData,
    mem::MaybeUninit,
    ops::Range,
    path::Path,
};

use ahash::AHashMap;
use anyhow::anyhow;
use libc::{dladdr, dlsym, Dl_info, RTLD_NEXT};
use tokio::runtime::Runtime;
use tracing::error;
use utils::kernel_symbols::CompactSystemMap;
use wholesym::{
    samply_symbols::object::{
        macho::{MachHeader64, CPU_SUBTYPE_ARM64E},
        read::macho::MachHeader,
        LittleEndian,
    },
    LookupAddress, MultiArchDisambiguator, SymbolManager, SymbolManagerConfig, SymbolMap,
};

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

pub struct DladdrSymbolicator {
    _private: PhantomData<()>,
}

impl DladdrSymbolicator {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            _private: PhantomData,
        })
    }
}

impl Symbolicator for DladdrSymbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        let mut info = MaybeUninit::<Dl_info>::uninit();
        let ret = unsafe { dladdr(addr as *const c_void, info.as_mut_ptr()) };
        if ret == 0 {
            error!("dladdr failed for address {:x}", addr);
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
            error!(
                "no symbol for address {:x} image={} base={:x}",
                addr, image, image_base
            );
            None
        };

        Ok(Some(SymbolResult {
            image,
            image_base,
            symbol_offset,
        }))
    }

    fn symbol_range(&mut self, name: &str) -> anyhow::Result<Option<Range<usize>>> {
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

pub struct CachedSymbolicator<S> {
    cache: AHashMap<u64, Option<SymbolResult>>,
    inner: S,
}

impl<S: Symbolicator> CachedSymbolicator<S> {
    pub fn new(inner: S) -> Self {
        Self {
            cache: AHashMap::new(),
            inner,
        }
    }
}

impl<S: Symbolicator> Symbolicator for CachedSymbolicator<S> {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        if let Some(symbol) = self.cache.get(&addr) {
            return Ok(symbol.clone());
        }

        let symbol = self.inner.addr_to_symbol(addr)?;
        self.cache.insert(addr, symbol.clone());
        Ok(symbol)
    }
}

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
