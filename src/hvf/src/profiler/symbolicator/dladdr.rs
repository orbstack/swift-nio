use std::{
    ffi::{c_void, CStr, CString},
    marker::PhantomData,
    mem::MaybeUninit,
    ops::Range,
};

use libc::{dladdr, dlsym, Dl_info, RTLD_NEXT};
use smallvec::smallvec;
use tracing::error;

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
