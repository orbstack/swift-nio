use std::{mem::offset_of, ops::Range};

use anyhow::anyhow;
use arch_gen::x86::bootparam::boot_params;
use vm_memory::ByteValued;

use super::BootParamsWrapper;

const KERNEL_LOAD_ADDR: u64 = 0x1000000;
const KERNEL_64BIT_ENTRY_OFFSET: u64 = 0x200;

pub struct KernelLoadInfo {
    pub load_range: Range<usize>,
    pub guest_addr: u64,
    pub entry_addr: u64,
    pub params: BootParamsWrapper,
}

pub fn load_bzimage(bzimage: &[u8]) -> anyhow::Result<KernelLoadInfo> {
    let mut params = BootParamsWrapper(boot_params::default());

    // The start of setup header is defined by its offset within boot_params (0x01f1).
    let setup_header_start = offset_of!(boot_params, hdr);

    // Per x86 Linux 64-bit boot protocol:
    // "The end of setup header can be calculated as follows: 0x0202 + byte value at offset 0x0201"
    let setup_header_end = 0x0202 + bzimage[0x0201] as usize;

    // Read `setup_header` into `boot_params`. The bzImage may have a different size of
    // `setup_header`, so read directly into a byte slice of the outer `boot_params` structure
    // rather than reading into `params.hdr`. The bounds check in `.get_mut()` will ensure we do not
    // read beyond the end of `boot_params`.
    params
        .as_mut_slice()
        .get_mut(setup_header_start..setup_header_end)
        .ok_or_else(|| anyhow!("bad params bounds"))?
        .copy_from_slice(&bzimage[setup_header_start..setup_header_end]);

    // bzImage header signature "HdrS"
    if params.hdr.header != 0x53726448 {
        return Err(anyhow!("bad signature"));
    }

    let setup_sects = if params.hdr.setup_sects == 0 {
        4u64
    } else {
        params.hdr.setup_sects as u64
    };

    let kernel_offset = setup_sects
        .checked_add(1)
        .and_then(|sectors| sectors.checked_mul(512))
        .ok_or_else(|| anyhow!("bad setup_sects"))? as usize;
    let kernel_size = (params.hdr.syssize as usize)
        .checked_mul(16)
        .ok_or_else(|| anyhow!("bad syssize"))?;

    if kernel_offset + kernel_size > bzimage.len() {
        return Err(anyhow!("bad kernel offset/size"));
    }

    Ok(KernelLoadInfo {
        load_range: kernel_offset..kernel_offset + kernel_size,
        guest_addr: KERNEL_LOAD_ADDR,
        entry_addr: KERNEL_LOAD_ADDR + KERNEL_64BIT_ENTRY_OFFSET,
        params,
    })
}
