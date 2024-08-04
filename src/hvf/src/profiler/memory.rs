use std::mem::MaybeUninit;

use libc::{proc_pid_rusage, rusage_info_v0, rusage_info_v4, RUSAGE_INFO_V0, RUSAGE_INFO_V4};
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

const RUSAGE_INFO_V6: i32 = 6;

struct rusage_info_v6 {
    pub ri_uuid: [u8; 16],
    pub ri_user_time: u64,
    pub ri_system_time: u64,
    pub ri_pkg_idle_wkups: u64,
    pub ri_interrupt_wkups: u64,
    pub ri_pageins: u64,
    pub ri_wired_size: u64,
    pub ri_resident_size: u64,
    pub ri_phys_footprint: u64,
    pub ri_proc_start_abstime: u64,
    pub ri_proc_exit_abstime: u64,
    pub ri_child_user_time: u64,
    pub ri_child_system_time: u64,
    pub ri_child_pkg_idle_wkups: u64,
    pub ri_child_interrupt_wkups: u64,
    pub ri_child_pageins: u64,
    pub ri_child_elapsed_abstime: u64,
    pub ri_diskio_bytesread: u64,
    pub ri_diskio_byteswritten: u64,
    pub ri_cpu_time_qos_default: u64,
    pub ri_cpu_time_qos_maintenance: u64,
    pub ri_cpu_time_qos_background: u64,
    pub ri_cpu_time_qos_utility: u64,
    pub ri_cpu_time_qos_legacy: u64,
    pub ri_cpu_time_qos_user_initiated: u64,
    pub ri_cpu_time_qos_user_interactive: u64,
    pub ri_billed_system_time: u64,
    pub ri_serviced_system_time: u64,
    pub ri_logical_writes: u64,
    pub ri_lifetime_max_phys_footprint: u64,
    pub ri_instructions: u64,
    pub ri_cycles: u64,
    pub ri_billed_energy: u64,
    pub ri_serviced_energy: u64,
    pub ri_interval_max_phys_footprint: u64,
    pub ri_runnable_time: u64,
    pub ri_flags: u64,
    pub ri_user_ptime: u64,
    pub ri_system_ptime: u64,
    pub ri_pinstructions: u64,
    pub ri_pcycles: u64,
    pub ri_energy_nj: u64,
    pub ri_penergy_nj: u64,
    pub ri_secure_time_in_system: u64,
    pub ri_secure_ptime_in_system: u64,
    pub ri_reserved: [u64; 12],
}

pub struct RusageInfo {
    pub phys_footprint_bytes: u64,
    pub disk_io_bytes: u64,
    pub energy_nj: u64,
}

pub fn get_rusage_info(pid: i32) -> nix::Result<RusageInfo> {
    let mut info = MaybeUninit::<rusage_info_v6>::uninit();
    let ret = unsafe { proc_pid_rusage(pid, RUSAGE_INFO_V6, info.as_mut_ptr() as *mut _) };
    Errno::result(ret).map(|_| {
        let info = unsafe { info.assume_init() };
        RusageInfo {
            phys_footprint_bytes: info.ri_phys_footprint,
            disk_io_bytes: info.ri_diskio_bytesread + info.ri_diskio_byteswritten,
            energy_nj: info.ri_energy_nj,
        }
    })
}
