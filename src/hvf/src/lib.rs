#[cfg(target_arch = "x86_64")]
mod x86_64;
use vm_memory::{GuestAddress, GuestMemoryMmap, GuestRegionMmap, MmapRegion};
#[cfg(target_arch = "x86_64")]
pub use x86_64::*;

#[cfg(target_arch = "aarch64")]
mod aarch64;
#[cfg(target_arch = "aarch64")]
pub use aarch64::*;

mod hypercalls;

// TODO: unify all our libsystem externs into one package
mod sys {
    extern "C" {
        pub fn minherit(addr: *const libc::c_void, len: usize, inherit: libc::c_int)
            -> libc::c_int;
    }

    pub const VM_INHERIT_NONE: libc::c_int = 2;
}

fn minherit(addr: *const libc::c_void, len: usize, inherit: libc::c_int) -> nix::Result<()> {
    let ret = unsafe { sys::minherit(addr, len, inherit) };
    if ret == -1 {
        return Err(nix::Error::last());
    }
    Ok(())
}

// on macOS, use the HVF API to allocate guest memory. it seems to use mach APIs
// standard mmap causes 2x overaccounting in Activity Monitor's "Memory" tab
pub fn allocate_guest_memory(ranges: &[(GuestAddress, usize)]) -> anyhow::Result<GuestMemoryMmap> {
    let regions = ranges
        .iter()
        .map(|(guest_base, size)| {
            let host_addr = unsafe { vm_allocate(*size) }
                .map_err(|e| anyhow::anyhow!("failed to allocate host memory: {}", e))?;

            // with large amounts of vm_allocated space (e.g. ~64 GiB) in the process, fork takes a very long time (~1 sec)
            // disable inheritance to fix this
            // note: these are mach_vm_map regions so we're relying on an implementation detail, but it should be OK because ultimately everything (even mmap) is VM_ALLOCATE on macOS
            // minherit also does the same thing as mach_vm_inherit in XNU
            minherit(host_addr, *size, sys::VM_INHERIT_NONE)
                .map_err(|e| anyhow::anyhow!("failed to disable inheritance: {}", e))?;

            let region = unsafe {
                MmapRegion::build_raw(
                    host_addr as *mut u8,
                    *size,
                    libc::PROT_READ | libc::PROT_WRITE,
                    libc::MAP_ANONYMOUS | libc::MAP_NORESERVE | libc::MAP_PRIVATE,
                )
                .map_err(|e| anyhow::anyhow!("failed to create mmap region: {}", e))?
            };

            GuestRegionMmap::new(region, *guest_base)
                .map_err(|e| anyhow::anyhow!("failed to create guest memory region: {}", e))
        })
        .collect::<anyhow::Result<Vec<_>>>()?;

    Ok(GuestMemoryMmap::from_regions(regions)?)
}
