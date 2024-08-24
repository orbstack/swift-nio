use std::{thread::sleep, time::Duration};

use anyhow::anyhow;
use crossbeam_channel::Sender;
use libc::{c_void, madvise, VM_MAKE_TAG};
use mach2::{
    kern_return::KERN_SUCCESS,
    traps::mach_task_self,
    vm::{mach_vm_deallocate, mach_vm_map, mach_vm_remap},
    vm_inherit::VM_INHERIT_NONE,
    vm_page_size::vm_page_size,
    vm_prot::{VM_PROT_EXECUTE, VM_PROT_READ, VM_PROT_WRITE},
    vm_statistics::{VM_FLAGS_ANYWHERE, VM_FLAGS_FIXED, VM_FLAGS_OVERWRITE},
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use nix::errno::Errno;
use tracing::{debug_span, error};
use utils::Mutex;
use vm_memory::{Address, GuestAddress, GuestMemoryMmap, GuestRegionMmap, MmapRegion};

use crate::{HvfVm, MemoryFlags};

const MEMORY_REMAP_INTERVAL: Duration = Duration::from_secs(30);
const MEMORY_REGION_TAG: u8 = 250; // application specific tag space

const VM_FLAGS_4GB_CHUNK: i32 = 4;

// use smaller 4 MiB anon chunks for guest memory
// good for performance because faults that add a page take the object's write lock,
// which is especially relevant when we use REUSABLE to purge pages
const MACH_CHUNK_SIZE: usize = 4 * 1024 * 1024;

static HOST_PMAP: Mutex<HostPmap> = Mutex::new(HostPmap {});

/// Messages for requesting memory maps/unmaps.
pub enum MemoryMapping {
    AddMapping(Sender<bool>, usize, GuestAddress, usize),
    RemoveMapping(Sender<bool>, GuestAddress, usize),
}

struct HostPmap {}

impl HostPmap {
    unsafe fn remap(&self, host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
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
        if ret != KERN_SUCCESS {
            return Err(anyhow!("error {}", ret));
        }

        Ok(())
    }
}

pub(crate) fn page_size() -> usize {
    unsafe { vm_page_size }
}

unsafe fn new_chunks_at(host_base_addr: *mut c_void, total_size: usize) -> anyhow::Result<()> {
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
        if ret != KERN_SUCCESS {
            return Err(anyhow::anyhow!("allocate host memory: error {}", ret));
        }
    }

    Ok(())
}

// although this ends up calling madvise page-by-page, it's still faster to use a range as large as possible because of the HVF unmap/map part
/// # Safety
/// host_addr must be a mapped, contiguous host memory region of at least `size` bytes
pub unsafe fn free_range(
    hvf_vm: &HvfVm,
    guest_addr: GuestAddress,
    host_addr: *mut c_void,
    size: usize,
) -> anyhow::Result<()> {
    // start and end must be page-aligned
    if host_addr as usize % page_size() != 0 {
        return Err(anyhow!("unaligned address: {:x}", host_addr as usize));
    }
    if size % page_size() != 0 {
        return Err(anyhow!("unaligned size: {}", size));
    }

    // clear this range from hv pmap ledger:
    // hv_vm_protect(NONE) then (RWX) is faster but not reliable if swapping already caused pages to be removed from pmap
    hvf_vm.unmap_memory(guest_addr, size)?;

    // call madvise on each individual page, not as one large chunk
    // this bypasses the pmap_clear_refmod_range_options optimization, which requires that pages are mapped in our pmap (which they aren't, because of periodic remapping)
    // pmap ref/mod is the source of truth, so it prevents pageout despite vmp_dirty=FALSE
    // this looks slow, but it's faster than the alternative of retouching/prefaulting each page to map it, which also causes memory pressure spikes if swapping
    for addr in (host_addr as usize..host_addr as usize + size).step_by(page_size()) {
        let ret = madvise(addr as *mut c_void, page_size(), libc::MADV_FREE_REUSABLE);
        Errno::result(ret)?;
    }

    // remap memory after madvise(REUSABLE), to reduce how many pmaps that it has to modify
    hvf_vm.map_memory(host_addr as *mut u8, guest_addr, size, MemoryFlags::RWX)?;

    Ok(())
}

/// # Safety
/// host_addr must be a mapped, contiguous host memory region of at least `size` bytes
pub unsafe fn reuse_range(host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
    // start and end must be page-aligned
    if host_addr as usize % page_size() != 0 {
        return Err(anyhow!("unaligned address: {:x}", host_addr as usize));
    }
    if size % page_size() != 0 {
        return Err(anyhow!("unaligned size: {}", size));
    }

    // this can be one big call, as long as REUSABLE was set properly
    // it iterates through pages in the object's queue -- no pmap tricks
    let ret = madvise(host_addr, size, libc::MADV_FREE_REUSE);
    Errno::result(ret)?;

    Ok(())
}

fn vm_allocate(size: mach_vm_size_t) -> anyhow::Result<*mut c_void> {
    // reserve contiguous address space atomically, and hold onto it to prevent races until we're done mapping everything
    // this is ONLY for reserving address space; we never actually use this mapping
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
            VM_PROT_READ | VM_PROT_WRITE,
            VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE,
            // safe: we won't fork while mapping, and child won't be in the middle of this mapping code
            VM_INHERIT_NONE,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!("reserve host memory: error {}", ret));
    }

    // on failure, deallocate all chunks
    let map_guard = scopeguard::guard((), |_| {
        unsafe { mach_vm_deallocate(mach_task_self(), host_addr, size) };
    });

    // make smaller chunks
    unsafe { new_chunks_at(host_addr as *mut c_void, size as usize)? };

    // we've replaced all mach chunks, so no longer need to deallocate reserved space
    std::mem::forget(map_guard);

    // spawn thread to periodically remap and fix double accounting
    // TODO: stop this
    std::thread::Builder::new()
        // vague user-facing name
        .name("VMA".to_string())
        .spawn(move || loop {
            // ideally this thread should have background QoS, but that'd cause it to hold pmap/PPL locks for a long time while slowly processing the remap on an E core, which would contend with other CPUs
            sleep(MEMORY_REMAP_INTERVAL);

            // don't race with REUSABLE
            let pmap = HOST_PMAP.lock().unwrap();
            if let Err(e) = unsafe { pmap.remap(host_addr as *mut c_void, size as usize) } {
                error!("remap failed: {:?}", e);
            }
        })?;

    Ok(host_addr as *mut c_void)
}

pub fn allocate_guest_memory(ranges: &[(GuestAddress, usize)]) -> anyhow::Result<GuestMemoryMmap> {
    // allocate one big contiguous region on the host, so that there are no holes when
    // reading from guest memory. each size and base must be page-aligned
    let total_size = ranges.iter().map(|(_, size)| *size).sum::<usize>();
    let host_base_addr = vm_allocate(total_size as mach_vm_size_t)?;
    let mut host_cur_addr = host_base_addr;

    let regions = ranges
        .iter()
        .map(|(guest_base, size)| {
            // these two checks guarantee that host addr is also page-aligned
            if guest_base.raw_value() % page_size() as u64 != 0 {
                return Err(anyhow!(
                    "guest address must be page-aligned: {:x}",
                    guest_base.raw_value()
                ));
            }
            if size % page_size() != 0 {
                return Err(anyhow!("size must be page-aligned: {}", size));
            }

            let region = unsafe {
                MmapRegion::build_raw(
                    host_cur_addr as *mut u8,
                    *size,
                    libc::PROT_READ | libc::PROT_WRITE,
                    libc::MAP_ANON | libc::MAP_PRIVATE,
                )
            }
            .map_err(|e| anyhow!("create mmap region: {}", e))?;
            host_cur_addr = unsafe { host_cur_addr.add(*size) };

            GuestRegionMmap::new(region, *guest_base)
                .map_err(|e| anyhow!("create guest memory region: {}", e))
        })
        .collect::<anyhow::Result<Vec<_>>>()?;

    Ok(GuestMemoryMmap::from_regions(regions)?)
}
