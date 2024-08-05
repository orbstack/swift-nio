#[cfg(target_arch = "x86_64")]
mod x86_64;
use std::{sync::Arc, thread::sleep, time::Duration};

use anyhow::anyhow;
use crossbeam::queue::ArrayQueue;
use crossbeam_channel::Sender;
use gruel::{StartupAbortedError, StartupTask};
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
use profiler::{PartialSample, ProfilerGuestContext, ProfilerVcpuInit, VcpuProfilerResults};
use tracing::error;
use utils::Mutex;
use vm_memory::{Address, GuestAddress, GuestMemoryMmap, GuestRegionMmap, MmapRegion};
use vmm_ids::{ArcVcpuSignal, VcpuSignalMask};
#[cfg(target_arch = "x86_64")]
pub use x86_64::*;

#[cfg(target_arch = "aarch64")]
mod aarch64;
#[cfg(target_arch = "aarch64")]
pub use aarch64::*;

pub mod profiler;

const MEMORY_REMAP_INTERVAL: Duration = Duration::from_secs(30);
const MEMORY_REGION_TAG: u8 = 250; // application specific tag space

const VM_FLAGS_4GB_CHUNK: i32 = 4;

const MACH_CHUNK_SIZE: usize = 64 * 1024 * 1024; // 8 MiB

pub struct VcpuHandleInner {
    signal: ArcVcpuSignal,
    profiler_init: Mutex<Option<ProfilerVcpuInit>>,
    profiler_sample: ArrayQueue<PartialSample>,
    profiler_guest_fetch: Mutex<Option<Sender<ProfilerGuestContext>>>,
    profiler_finish: Mutex<Option<Sender<VcpuProfilerResults>>>,
}

impl VcpuHandleInner {
    pub fn new(signal: ArcVcpuSignal) -> Self {
        Self {
            signal,
            profiler_init: Mutex::new(None),
            profiler_sample: ArrayQueue::new(1),
            profiler_guest_fetch: Mutex::new(None),
            profiler_finish: Mutex::new(None),
        }
    }

    pub fn pause(&self) {
        self.signal.assert(VcpuSignalMask::PAUSE);
    }

    pub fn dump_debug(&self) {
        self.signal.assert(VcpuSignalMask::DUMP_DEBUG);
    }

    pub fn send_profiler_init(&self, init: ProfilerVcpuInit) {
        *self.profiler_init.lock().unwrap() = Some(init);
        self.signal.assert(VcpuSignalMask::PROFILER_INIT);
    }

    pub fn send_profiler_sample(&self, sample: PartialSample) {
        self.profiler_sample.force_push(sample);
        self.signal.assert(VcpuSignalMask::PROFILER_SAMPLE);
    }

    pub fn send_profiler_guest_fetch(&self, sender: Sender<ProfilerGuestContext>) {
        *self.profiler_guest_fetch.lock().unwrap() = Some(sender);
        self.signal.assert(VcpuSignalMask::PROFILER_GUEST_FETCH);
    }

    pub fn send_profiler_finish(&self, sender: Sender<VcpuProfilerResults>) {
        *self.profiler_finish.lock().unwrap() = Some(sender);
        self.signal.assert(VcpuSignalMask::PROFILER_FINISH);
    }

    pub fn consume_profiler_init(&self) -> Option<ProfilerVcpuInit> {
        self.profiler_init.lock().unwrap().take()
    }

    pub fn consume_profiler_sample(&self) -> Option<PartialSample> {
        self.profiler_sample.pop()
    }

    pub fn consume_profiler_guest_fetch(&self) -> Option<Sender<ProfilerGuestContext>> {
        self.profiler_guest_fetch.lock().unwrap().take()
    }

    pub fn consume_profiler_finish(&self) -> Option<Sender<VcpuProfilerResults>> {
        self.profiler_finish.lock().unwrap().take()
    }
}

pub type ArcVcpuHandle = Arc<VcpuHandleInner>;

pub trait VcpuRegistry: Send + Sync {
    fn park(&self) -> Result<StartupTask, StartupAbortedError>;

    fn unpark(&self, unpark_task: StartupTask);

    fn register_vcpu(&self, id: u8, vcpu: ArcVcpuHandle) -> StartupTask;

    fn num_vcpus(&self) -> usize;

    fn get_vcpu(&self, id: u8) -> Option<ArcVcpuHandle>;

    fn process_park_commands(
        &self,
        taken: VcpuSignalMask,
        park_task: StartupTask,
    ) -> Result<StartupTask, StartupAbortedError>;

    fn dump_debug(&self);
}

fn page_size() -> usize {
    unsafe { vm_page_size }
}

unsafe fn remap_region(host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
    // clear double accounting
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

unsafe fn new_chunks_at(host_base_addr: *mut c_void, total_size: usize) -> anyhow::Result<()> {
    let host_end_addr = host_base_addr as usize + total_size;
    for addr in (host_base_addr as usize..host_end_addr).step_by(MACH_CHUNK_SIZE) {
        let mut entry_addr = addr as mach_vm_address_t;
        let entry_size = std::cmp::min(MACH_CHUNK_SIZE, host_end_addr - addr) as mach_vm_size_t;

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

/// # Safety
/// host_addr must be a mapped, contiguous host memory region of at least `size` bytes
pub unsafe fn free_range(
    guest_addr: GuestAddress,
    host_addr: *mut c_void,
    size: usize,
) -> anyhow::Result<()> {
    // let _span = tracing::info_span!("free_range", size = size).entered();
    // start and end must be page-aligned
    if host_addr as usize % page_size() != 0 {
        return Err(anyhow!(
            "address must be page-aligned: {:x}",
            host_addr as usize
        ));
    }
    if size % page_size() != 0 {
        return Err(anyhow!("size must be page-aligned: {}", size));
    }

    // madvise on host address
    let ret = madvise(host_addr, size, libc::MADV_FREE_REUSABLE);
    Errno::result(ret).map_err(|e| anyhow!("free: {}", e))?;

    // clear this range from hv pmap ledger:
    // there's no other way to clear from hv pmap, and we *will* incur this cost at some point
    // hv_vm_protect(0) then (RWX) is slightly faster than unmap+map, and does the same thing (including split+coalesce)
    HvfVm::protect_memory_static(guest_addr.raw_value(), size as u64, 0)?;
    HvfVm::protect_memory_static(
        guest_addr.raw_value(),
        size as u64,
        (HV_MEMORY_READ | HV_MEMORY_WRITE | HV_MEMORY_EXEC) as _,
    )?;

    Ok(())
}

/// # Safety
/// host_addr must be a mapped, contiguous host memory region of at least `size` bytes
pub unsafe fn reuse_range(host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
    // let _span = tracing::info_span!("free_range", size = size).entered();
    // start and end must be page-aligned
    if host_addr as usize % page_size() != 0 {
        return Err(anyhow!(
            "address must be page-aligned: {:x}",
            host_addr as usize
        ));
    }
    if size % page_size() != 0 {
        return Err(anyhow!("size must be page-aligned: {}", size));
    }

    // madvise on host address
    let ret = madvise(host_addr, size, libc::MADV_FREE_REUSE);
    Errno::result(ret).map_err(|e| anyhow!("reuse: {}", e))?;

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
    std::thread::spawn(move || loop {
        sleep(MEMORY_REMAP_INTERVAL);

        // TODO: stop this
        if let Err(e) = unsafe { remap_region(host_addr as *mut c_void, size as usize) } {
            error!("remap failed: {:?}", e);
        }
    });

    Ok(host_addr as *mut c_void)
}

// on macOS, use the HVF API to allocate guest memory. it seems to use mach APIs
// standard mmap causes 2x overaccounting in Activity Monitor's "Memory" tab
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
