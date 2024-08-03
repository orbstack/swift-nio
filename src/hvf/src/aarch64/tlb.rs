use std::ptr::NonNull;

use ahash::AHashMap;
use anyhow::anyhow;
use utils::memory::GuestMemoryExt;
use vm_memory::{ByteValued, GuestMemoryMmap};

use super::HvfVcpu;

// fused TLB + host mmap address lookup + bounds check for a specific type
pub struct Tlb<V: ByteValued> {
    guest_mem: GuestMemoryMmap,
    cache: AHashMap<u64, Option<NonNull<V>>>,
}

// the cached pointer are Send because we have GuestMemoryMmap and we only read from it
unsafe impl<V: ByteValued> Send for Tlb<V> {}

impl<V: ByteValued> Tlb<V> {
    pub fn new(guest_mem: GuestMemoryMmap) -> Self {
        Self {
            guest_mem,
            cache: AHashMap::new(),
        }
    }

    fn get_host_addr(&mut self, vcpu: &HvfVcpu, vaddr: u64) -> Option<NonNull<V>> {
        if let Some(&host_addr) = self.cache.get(&vaddr) {
            return host_addr;
        }

        let paddr = vcpu.translate_gva(vaddr).ok()?;
        let res = self
            .guest_mem
            .get_obj_ptr_aligned(paddr)
            .ok()
            // null is not a valid host pointer
            .map(|ptr| unsafe { NonNull::new_unchecked(ptr) });

        self.cache.insert(vaddr, res);
        res
    }

    pub fn read_obj(&mut self, vcpu: &HvfVcpu, vaddr: u64) -> anyhow::Result<V> {
        let host_addr = self
            .get_host_addr(vcpu, vaddr)
            .ok_or_else(|| anyhow!("failed to read memory at {:#x}", vaddr))?;
        Ok(unsafe { host_addr.as_ptr().read() })
    }
}
