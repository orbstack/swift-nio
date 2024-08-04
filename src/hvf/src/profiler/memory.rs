use std::mem::MaybeUninit;

use libc::{proc_pid_rusage, rusage_info_v0, RUSAGE_INFO_V0};
use nix::errno::Errno;
use vm_memory::ByteValued;

// no real address can be in __PAGEZERO (which is the full 32-bit space)
pub const MIN_ADDR: u64 = 0x100000000;

// mask out PAC signature, assuming 47-bit VA (machdep.virtual_address_size)
pub const PAC_MASK: u64 = u64::MAX >> 17;

// unsafe: this attempts to do some basic validation, which catches most invalid addresses we see in PC/LR or on the stack
// we could set a per-thread Mach exception port on the profiler thread to catch invalid memory accesses, but that's more work
// mach_vm_read() is far too slow since it makes a syscall for every read
// another way is to get a list of valid regions, but that's error-prone in case of allocations, and slow
// invalid addresses should be very rare so exception ports are the ideal solution
#[inline]
pub unsafe fn read_host_mem_aligned<T: ByteValued>(addr: u64) -> Option<T> {
    if is_valid_address(addr) {
        Some(unsafe { (addr as *const T).read() })
    } else {
        None
    }
}

#[inline]
pub const fn is_valid_address(addr: u64) -> bool {
    addr >= MIN_ADDR && (addr & !PAC_MASK == 0)
}

pub fn get_phys_footprint(pid: i32) -> nix::Result<u64> {
    let mut info = MaybeUninit::<rusage_info_v0>::uninit();
    let ret = unsafe { proc_pid_rusage(pid, RUSAGE_INFO_V0, info.as_mut_ptr() as *mut _) };
    Errno::result(ret).map(|_| unsafe { info.assume_init().ri_phys_footprint })
}
