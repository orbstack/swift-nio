use std::{arch::asm, ffi::c_void, sync::LazyLock};

use utils::macos::sysctl::OS_VERSION;

use super::{
    bindings::{hv_ipa_t, hv_memory_flags_t, hv_return_t, hv_vm_map, hv_vm_protect, hv_vm_unmap},
    ENABLE_NESTED_VIRT, USE_HVF_GIC,
};

const HV_ID_VM_MAP_SPACE: u64 = 3;
const HV_ID_VM_UNMAP_SPACE: u64 = 4;
const HV_ID_VM_PROTECT_SPACE: u64 = 5;

/*
 * memory mapping workaround (FB15459210)
 *
 * on macOS 15, hv_vm_unmap splits ranges but doesn't coalesce on remap, and all ranges are stored in a C++ std::vector.
 * this causes O(N^2) behavior that burns up to >7 seconds of CPU (worsening with uptime) every time balloon runs.
 *
 * as a workaround, use the raw syscall interface on known-broken macOS versions where the ABI is also known.
 */
static USE_MEM_MAP_WORKAROUND: LazyLock<bool> = LazyLock::new(|| {
    if USE_HVF_GIC || ENABLE_NESTED_VIRT {
        // HVF GICv3 and EL2 nested virt make use of the userspace memory map, making this workaround unsafe
        false
    } else {
        // macOS 15.0-15.2 (tested on beta 1 [24C5057p])
        OS_VERSION.major == 15 && OS_VERSION.minor <= 2
    }
});

// private API
#[allow(non_camel_case_types)]
type hv_space_t = *mut c_void;

#[repr(C)]
struct VmMapArgs {
    addr: *mut c_void,
    ipa: hv_ipa_t,
    size: usize,
    flags: hv_memory_flags_t,
    space: hv_space_t,
}

// hv_trap is unexported so we can't link against it
unsafe fn call_hv(id: u64, arg: u64) -> hv_return_t {
    // negative for Mach, 0x5 for hypervisor
    let mach_trap: i64 = -0x5;
    let ret: u64;
    let _ret2: u64;
    // arm64 XNU syscall ABI: x16 = syscall number, immediate = 0x80, return = x0 + x1 + carry flag
    asm!("svc #0x80", in("x0") id, in("x1") arg, in("x16") mach_trap, lateout("x0") ret, lateout("x1") _ret2);
    ret as hv_return_t
}

pub unsafe fn vm_map(
    addr: *mut c_void,
    ipa: hv_ipa_t,
    size: usize,
    flags: hv_memory_flags_t,
) -> hv_return_t {
    if *USE_MEM_MAP_WORKAROUND {
        let args = VmMapArgs {
            addr,
            ipa,
            size,
            flags,
            space: std::ptr::null_mut(),
        };
        call_hv(HV_ID_VM_MAP_SPACE, &args as *const _ as u64)
    } else {
        unsafe { hv_vm_map(addr, ipa, size, flags) }
    }
}

pub unsafe fn vm_unmap(ipa: hv_ipa_t, size: usize) -> hv_return_t {
    if *USE_MEM_MAP_WORKAROUND {
        let args = VmMapArgs {
            ipa,
            size,
            flags: 0,
            addr: std::ptr::null_mut(),
            space: std::ptr::null_mut(),
        };
        call_hv(HV_ID_VM_UNMAP_SPACE, &args as *const _ as u64)
    } else {
        unsafe { hv_vm_unmap(ipa, size) }
    }
}

pub unsafe fn vm_protect(ipa: hv_ipa_t, size: usize, flags: hv_memory_flags_t) -> hv_return_t {
    if *USE_MEM_MAP_WORKAROUND {
        let args = VmMapArgs {
            ipa,
            size,
            flags,
            addr: std::ptr::null_mut(),
            space: std::ptr::null_mut(),
        };
        call_hv(HV_ID_VM_PROTECT_SPACE, &args as *const _ as u64)
    } else {
        unsafe { hv_vm_protect(ipa, size, flags) }
    }
}
