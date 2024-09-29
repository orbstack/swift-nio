use std::{
    ffi::{c_void, CStr, CString},
    marker::PhantomData,
    mem::MaybeUninit,
    ops::Range,
};

use anyhow::anyhow;
use libc::{dladdr, dlsym, Dl_info, RTLD_NEXT};
use smallvec::smallvec;
use tracing::error;

use crate::profiler::{arch, dyld};

use super::{SymbolFunc, SymbolResult, SymbolResults, Symbolicator};

pub struct DladdrSymbolicator {
    _private: PhantomData<()>,
}

impl DladdrSymbolicator {
    pub fn new() -> anyhow::Result<Self> {
        Ok(Self {
            _private: PhantomData,
        })
    }

    /*
     * dlsym only works on exported symbols.
     * symtab isn't in __TEXT, so it's not mapped by dyld.
     * HVF is in the dyld shared cache, so it's hard to load the symtab from Mach-O directly.
     *
     * however, dladdr can resolve addresses to unexported symbols, and HVF is a small library,
     * so we can just brute-force every possible address in the mapped image __TEXT segment
     * to find the start and end of an unexported symbol.
     */
    pub fn symbol_range_in_image(
        &self,
        image: &str,
        name: &str,
    ) -> anyhow::Result<Option<Range<usize>>> {
        let img = dyld::get_loaded_images()?
            .into_iter()
            .find(|img| img.path.rsplit_once('/').unwrap().1 == image)
            .ok_or(anyhow!("image not found: {}", image))?;

        // scan from image start to end
        let mut symbol_start = None;
        for addr in
            (img.addr_range.start..img.addr_range.end).step_by(arch::ARM64_INSN_SIZE as usize)
        {
            let mut info = MaybeUninit::<Dl_info>::uninit();
            let ret = unsafe { dladdr(addr as *const _, info.as_mut_ptr()) };
            if ret == 0 {
                continue;
            }

            let info = unsafe { info.assume_init() };
            if info.dli_sname.is_null() {
                // no longer in the target symbol
                if let Some(start) = symbol_start {
                    return Ok(Some(start..addr));
                }
                continue;
            }

            let symbol = unsafe { CStr::from_ptr(info.dli_sname) }.to_string_lossy();
            if symbol != name {
                // no longer in the target symbol
                if let Some(start) = symbol_start {
                    return Ok(Some(start..addr));
                }
                continue;
            }

            if symbol_start.is_none() {
                // found start of symbol!
                symbol_start = Some(addr);
            }
        }

        // fallthrough = target symbol extends to end of image
        if let Some(start) = symbol_start {
            Ok(Some(start..img.addr_range.end))
        } else {
            Ok(None)
        }
    }
}

impl Symbolicator for DladdrSymbolicator {
    fn addr_to_symbols(&mut self, addr: u64) -> anyhow::Result<SymbolResults> {
        let mut info: MaybeUninit<Dl_info> = MaybeUninit::<Dl_info>::uninit();
        let ret = unsafe { dladdr(addr as *const c_void, info.as_mut_ptr()) };
        if ret == 0 {
            error!("dladdr failed for address {:x}", addr);
            return Ok(smallvec![]);
        }
        let info = unsafe { info.assume_init() };

        let image = unsafe { CStr::from_ptr(info.dli_fname) }
            .to_string_lossy()
            .to_string();
        let image_base = info.dli_fbase as u64;

        let function = if !info.dli_sname.is_null() {
            let symbol = unsafe { CStr::from_ptr(info.dli_sname) }.to_string_lossy();
            let offset = (addr - info.dli_saddr as u64) as usize;
            let demangled = symbolic_demangle::demangle(&symbol);
            Some(SymbolFunc::Function(demangled.to_string(), offset))
        } else {
            error!(
                "no symbol for address {:x} image={} base={:x}",
                addr, image, image_base
            );
            None
        };

        Ok(smallvec![SymbolResult {
            image,
            image_base,
            function,
            source: None,
        }])
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
