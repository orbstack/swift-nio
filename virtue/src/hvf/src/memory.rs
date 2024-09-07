use std::{ptr::NonNull, sync::LazyLock, thread::sleep, time::Duration};

use anyhow::anyhow;
use crossbeam_channel::Sender;
use libc::{c_void, madvise, vm_deallocate, VM_MAKE_TAG};
use mach2::{
    kern_return::KERN_SUCCESS,
    traps::{current_task, mach_task_self},
    vm::{mach_vm_deallocate, mach_vm_map, mach_vm_remap},
    vm_inherit::VM_INHERIT_NONE,
    vm_page_size::vm_page_size,
    vm_prot::{VM_PROT_EXECUTE, VM_PROT_READ, VM_PROT_WRITE},
    vm_statistics::{VM_FLAGS_ANYWHERE, VM_FLAGS_FIXED, VM_FLAGS_OVERWRITE},
    vm_types::{mach_vm_address_t, mach_vm_size_t},
};
use nix::errno::Errno;
use sysx::mach::{
    error::MachError,
    time::{MachAbsoluteDuration, MachAbsoluteTime},
    timer::Timer,
};
use tracing::{debug_span, error};
use utils::{
    memory::{GuestAddress, GuestMemory},
    qos::QosClass,
    Mutex,
};

use crate::{HvfVm, MemoryFlags};

// tag to identify memory in vmmap/footprint
const MEMORY_REGION_TAG: u8 = 250; // application specific tag space

const VM_FLAGS_4GB_CHUNK: i32 = 4;

// use smaller 4 MiB anon chunks for guest memory
// good for performance because faults that add a page take the object's write lock,
// which is especially relevant when we use REUSABLE to purge pages
const MACH_CHUNK_SIZE: usize = 4 * 1024 * 1024;

// to clear double accounting caused by host touching guest memory for virtio,
// we remap every 30 sec, even if balloon isn't doing anything
const BACKGROUND_REMAP_INTERVAL: Duration = Duration::from_secs(30);

// we also remap before every free_range batch, but it's debounced to amortize the cost (~18ms for 12 GiB if pmapped)
const REMAP_DEBOUNCE_INTERVAL: Duration = Duration::from_millis(250);
static REMAP_DEBOUNCE_TIMER: LazyLock<Timer> = LazyLock::new(|| Timer::new().unwrap());

static HOST_PMAP: Mutex<HostPmap> = Mutex::new(HostPmap {
    last_remapped_at: MachAbsoluteTime::zero(),
    timer_armed: false,
    guest_mem: None,
});

/// Messages for requesting memory maps/unmaps.
pub enum MemoryMapping {
    AddMapping(Sender<bool>, usize, GuestAddress, usize),
    RemoveMapping(Sender<bool>, GuestAddress, usize),
}

struct HostPmap {
    last_remapped_at: MachAbsoluteTime,
    timer_armed: bool,

    guest_mem: Option<(*mut c_void, usize)>,
}

unsafe impl Send for HostPmap {}

impl HostPmap {
    unsafe fn set_guest_mem(&mut self, host_addr: *mut c_void, size: usize) {
        self.guest_mem = Some((host_addr, size));
    }

    pub unsafe fn maybe_remap(&mut self) -> anyhow::Result<()> {
        // leading edge debounce: remap synchronously if enough time has passed, otherwise arm a timer
        let now = MachAbsoluteTime::now();
        if (now - self.last_remapped_at).as_duration() > REMAP_DEBOUNCE_INTERVAL {
            let (host_addr, size) = self.guest_mem.ok_or_else(|| anyhow!("no guest memory"))?;
            self.remap(host_addr, size)?;
        } else if !self.timer_armed {
            REMAP_DEBOUNCE_TIMER
                .arm_for(MachAbsoluteDuration::from_duration(REMAP_DEBOUNCE_INTERVAL))
                .unwrap();
            self.timer_armed = true;
        }

        Ok(())
    }

    unsafe fn remap(&mut self, host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
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

        self.last_remapped_at = MachAbsoluteTime::now();
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
    // this also makes madvise faster by reducing pmap work (no need to set fast fault flags and invalidate TLB)
    hvf_vm.unmap_memory(guest_addr, size)?;

    // call madvise on each individual page, not as one large chunk
    // this bypasses the pmap_clear_refmod_range_options optimization, which requires that pages are mapped in our pmap (which they aren't, because of periodic remapping)
    // pmap ref/mod is the source of truth, so it prevents pageout despite vmp_dirty=FALSE
    // this looks slow, but it's faster than the alternative of retouching/prefaulting each page to map it, which also causes memory pressure spikes if swapping
    for addr in (host_addr as usize..host_addr as usize + size).step_by(page_size()) {
        let ret = madvise(addr as *mut c_void, page_size(), libc::MADV_FREE);
        Errno::result(ret)?;
    }

    // leave it unmapped for madvise, so that it looks like a normal private memory object
    hvf_vm.map_memory(host_addr as *mut u8, guest_addr, size, MemoryFlags::RWX)?;

    Ok(())
}

// we need to remap when freeing, to clear ledgers, but it doesn't matter whether we do it before/after
// so, to reduce pmap work and make madvise faster, do it before (debounced to amortize cost)
pub fn maybe_remap() -> anyhow::Result<()> {
    unsafe { HOST_PMAP.lock().unwrap().maybe_remap() }
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

    unsafe {
        HOST_PMAP
            .lock()
            .unwrap()
            .set_guest_mem(host_addr as *mut c_void, size as usize);
    }

    // spawn thread to periodically remap and fix double accounting
    // TODO: stop this
    std::thread::Builder::new()
        // vague user-facing name
        .name("VMA 1".to_string())
        .spawn(move || {
            utils::qos::set_thread_qos(QosClass::Background, None).unwrap();

            loop {
                sleep(BACKGROUND_REMAP_INTERVAL);

                // kick debounce to avoid redundant remaps within a short period of time
                unsafe {
                    HOST_PMAP.lock().unwrap().maybe_remap().unwrap();
                }
            }
        })?;

    // thread to consume debounce events and do the actual remapping
    std::thread::Builder::new()
        // vague user-facing name
        .name("VMA 2".to_string())
        .spawn(move || loop {
            REMAP_DEBOUNCE_TIMER.wait();

            // ideally this thread should have background QoS, but that'd cause it to hold pmap/PPL locks for a long time while slowly processing the remap on an E core, which would contend with other CPUs
            let mut pmap = HOST_PMAP.lock().unwrap();
            if let Err(e) = unsafe { pmap.maybe_remap() } {
                error!("remap failed: {:?}", e);
            }
        })?;

    Ok(host_addr as *mut c_void)
}

pub fn allocate_guest_memory(size: usize) -> anyhow::Result<GuestMemory> {
    let base = vm_allocate(size as mach_vm_size_t)?;
    let unreserve = {
        let base = base as usize; // raw pointers are `!Send`.
        move || {
            let res = MachError::result(unsafe { vm_deallocate(current_task(), base, size) });
            if let Err(err) = res {
                tracing::error!("Failed to deallocate guest memory: {err}");
            }
        }
    };

    Ok(unsafe {
        GuestMemory::new(
            NonNull::slice_from_raw_parts(NonNull::new_unchecked(base).cast(), size),
            unreserve,
        )
    })
}
