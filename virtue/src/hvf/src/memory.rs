use std::{
    sync::{Arc, LazyLock},
    thread::sleep,
    time::Duration,
};

use anyhow::anyhow;
use crossbeam_channel::Sender;
use libc::{c_void, madvise};
use mach2::vm_page_size::vm_page_size;
use nix::errno::Errno;
use sysx::mach::{
    time::{MachAbsoluteDuration, MachAbsoluteTime},
    timer::Timer,
};
use tracing::error;
use utils::{
    memory::{GuestAddress, GuestMemory, MachVmGuestMemoryProvider},
    qos::QosClass,
    Mutex,
};

use crate::{HvfVm, MemoryFlags};

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

    guest_mem: Option<Arc<MachVmGuestMemoryProvider>>,
}

unsafe impl Send for HostPmap {}

impl HostPmap {
    fn set_guest_mem(&mut self, memory: Arc<MachVmGuestMemoryProvider>) {
        self.guest_mem = Some(memory);
    }

    pub unsafe fn maybe_remap(&mut self) -> anyhow::Result<()> {
        // leading edge debounce: remap synchronously if enough time has passed, otherwise arm a timer
        let now = MachAbsoluteTime::now();
        if (now - self.last_remapped_at).as_duration() > REMAP_DEBOUNCE_INTERVAL {
            self.guest_mem
                .as_ref()
                .ok_or_else(|| anyhow!("no guest memory"))?
                .remap()?;
        } else if !self.timer_armed {
            REMAP_DEBOUNCE_TIMER
                .arm_for(MachAbsoluteDuration::from_duration(REMAP_DEBOUNCE_INTERVAL))
                .unwrap();
            self.timer_armed = true;
        }

        Ok(())
    }
}

pub(crate) fn page_size() -> usize {
    unsafe { vm_page_size }
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

pub fn allocate_guest_memory(size: usize) -> anyhow::Result<GuestMemory> {
    let memory = Arc::new(MachVmGuestMemoryProvider::new(size)?);

    HOST_PMAP.lock().unwrap().set_guest_mem(memory.clone());

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

    Ok(GuestMemory::new(memory))
}
