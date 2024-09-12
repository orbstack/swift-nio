use std::{any::Any, mem, ptr::NonNull};

use anyhow::Context as _;
use libc::VM_MAKE_TAG;
use mach2::{
    traps::mach_task_self,
    vm::{mach_vm_deallocate, mach_vm_map, mach_vm_remap},
    vm_inherit::VM_INHERIT_NONE,
    vm_prot::{VM_PROT_EXECUTE, VM_PROT_NONE, VM_PROT_READ, VM_PROT_WRITE},
    vm_statistics::{VM_FLAGS_ANYWHERE, VM_FLAGS_FIXED, VM_FLAGS_OVERWRITE},
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use sysx::mach::error::MachError;
use tracing::debug_span;

use super::{GuestAddress, GuestMemoryProvider};

// === Core === //

#[derive(Debug)]
pub struct MachVmGuestMemoryProvider {
    host_addr: *mut libc::c_void,
    size: usize,
    ram_regions: Vec<(GuestAddress, usize)>,
}

unsafe impl Send for MachVmGuestMemoryProvider {}
unsafe impl Sync for MachVmGuestMemoryProvider {}

impl MachVmGuestMemoryProvider {
    pub fn new(ram_regions: &[(GuestAddress, usize)]) -> anyhow::Result<Self> {
        let &(last_base_addr, last_size) = ram_regions.last().unwrap();
        let size = last_base_addr.0 as usize + last_size;
        let host_addr = vm_allocate(size as mach_vm_size_t, ram_regions)?;

        Ok(Self {
            host_addr,
            size,
            ram_regions: ram_regions.to_vec(),
        })
    }

    pub fn remap(&self) -> anyhow::Result<()> {
        for &(base, size) in &self.ram_regions {
            unsafe {
                vm_remap(self.host_addr.cast::<u8>().add(base.usize()).cast(), size)?;
            }
        }

        Ok(())
    }
}

unsafe impl GuestMemoryProvider for MachVmGuestMemoryProvider {
    fn as_ptr(&self) -> NonNull<[u8]> {
        NonNull::slice_from_raw_parts(
            NonNull::new(self.host_addr).unwrap().cast::<u8>(),
            self.size,
        )
    }

    fn as_any(&self) -> &(dyn Any + Send + Sync) {
        self
    }
}

impl Drop for MachVmGuestMemoryProvider {
    fn drop(&mut self) {
        // TODO: implement a proper destructor for this
    }
}

// === Helpers === //

/// Tag to identify memory in vmmap/footprint
const MEMORY_REGION_TAG: u8 = 250; // (application specific tag space)

const VM_FLAGS_4GB_CHUNK: i32 = 4;

// use smaller 4 MiB anon chunks for guest memory
// good for performance because faults that add a page take the object's write lock,
// which is especially relevant when we use REUSABLE to purge pages
const MACH_CHUNK_SIZE: usize = 4 * 1024 * 1024;

fn vm_allocate(
    size: mach_vm_size_t,
    ram_regions: &[(GuestAddress, usize)],
) -> anyhow::Result<*mut libc::c_void> {
    // Reserve contiguous address space atomically, and hold onto it to prevent races until we're
    // done mapping everything. This is ONLY for reserving address space; we never actually use this
    // mapping.
    let mut host_addr: mach_vm_address_t = 0;
    let ret = unsafe {
        mach_vm_map(
            mach_task_self(),
            &mut host_addr,
            size,
            0,
            // runtime perf doesn't matter: we'll never fault on these chunks, so use big chunks to speed up reservation
            VM_FLAGS_ANYWHERE | VM_FLAGS_4GB_CHUNK | VM_MAKE_TAG(MEMORY_REGION_TAG) as i32,
            0,
            0,
            0,
            VM_PROT_NONE,
            VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE,
            // safe: we won't fork while mapping, and child won't be in the middle of this mapping code
            VM_INHERIT_NONE,
        )
    };

    MachError::result(ret)
        .with_context(|| format!("failed to reserve host memory of size {size}"))?;

    // On failure, deallocate all chunks
    let map_guard = scopeguard::guard((), |_| {
        unsafe { mach_vm_deallocate(mach_task_self(), host_addr, size) };
    });

    // Make smaller chunks
    for &(base, size) in ram_regions {
        unsafe { new_ram_chunks_at((host_addr + base.u64()) as *mut libc::c_void, size)? };
    }

    // We've replaced all mach chunks, so no longer need to deallocate reserved space
    mem::forget(map_guard);

    Ok(host_addr as *mut libc::c_void)
}

unsafe fn new_ram_chunks_at(
    host_base_addr: *mut libc::c_void,
    total_size: usize,
) -> anyhow::Result<()> {
    let host_end_addr = host_base_addr as usize + total_size;
    for addr in (host_base_addr as usize..host_end_addr).step_by(MACH_CHUNK_SIZE) {
        let mut entry_addr = addr as mach_vm_address_t;
        let entry_size = std::cmp::min(MACH_CHUNK_SIZE, host_end_addr - addr) as mach_vm_size_t;

        // these are pmap-accounted regions, so they get double-accounted, but madvise works
        let ret = mach_vm_map(
            mach_task_self(),
            &mut entry_addr,
            entry_size,
            0,
            VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE | VM_MAKE_TAG(MEMORY_REGION_TAG) as i32,
            0,
            0,
            0,
            VM_PROT_READ | VM_PROT_WRITE,
            VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE,
            // safe: we won't fork while mapping, and child won't be in the middle of this mapping code
            VM_INHERIT_NONE,
        );

        MachError::result(ret).context("failed to chunk host memory")?;
    }

    Ok(())
}

unsafe fn vm_remap(host_addr: *mut libc::c_void, size: usize) -> anyhow::Result<()> {
    let _span = debug_span!("remap", size = size).entered();

    // clear double accounting
    // pmap_remove clears the contribution to phys_footprint, and pmap_enter on refault will add it back
    let mut target_address = host_addr as mach_vm_address_t;
    let mut cur_prot = VM_PROT_READ | VM_PROT_WRITE;
    let mut max_prot = VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE;
    let ret = mach_vm_remap(
        mach_task_self(),
        &mut target_address,
        size as mach_vm_size_t,
        0,
        VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE,
        mach_task_self(),
        host_addr as mach_vm_address_t,
        0,
        &mut cur_prot,
        &mut max_prot,
        VM_INHERIT_NONE,
    );

    MachError::result(ret).context("failed to map memory")?;

    Ok(())
}
