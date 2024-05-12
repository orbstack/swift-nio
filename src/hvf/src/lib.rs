#[cfg(target_arch = "x86_64")]
mod x86_64;
use vm_memory::{GuestAddress, GuestMemoryMmap, GuestRegionMmap, MmapRegion};
#[cfg(target_arch = "x86_64")]
pub use x86_64::*;

#[cfg(target_arch = "aarch64")]
mod aarch64;
#[cfg(target_arch = "aarch64")]
pub use aarch64::*;

// on macOS, use the HVF API to allocate guest memory. it seems to use mach APIs
// standard mmap causes 2x overaccounting in Activity Monitor's "Memory" tab
pub fn allocate_guest_memory(ranges: &[(GuestAddress, usize)]) -> anyhow::Result<GuestMemoryMmap> {
    let regions = ranges
        .iter()
        .map(|(guest_base, size)| {
            let host_addr = unsafe { vm_allocate(*size) }
                .map_err(|_| anyhow::anyhow!("failed to allocate host memory"))?;

            let region = unsafe {
                MmapRegion::build_raw(
                    host_addr as *mut u8,
                    *size,
                    libc::PROT_READ | libc::PROT_WRITE,
                    libc::MAP_ANONYMOUS | libc::MAP_NORESERVE | libc::MAP_PRIVATE,
                )
                .map_err(|_| anyhow::anyhow!("failed to create mmap region"))?
            };

            GuestRegionMmap::new(region, *guest_base)
                .map_err(|_| anyhow::anyhow!("failed to create guest memory region"))
        })
        .collect::<anyhow::Result<Vec<_>>>()?;

    Ok(GuestMemoryMmap::from_regions(regions)?)
}
