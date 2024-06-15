#[cfg(target_arch = "x86_64")]
mod x86_64;
use std::{cell::Cell, mem::size_of, process::Command};

use libc::{c_void, mach_host_self, madvise, memory_object_t, VM_FLAGS_PURGABLE, VM_MAKE_TAG};
use mach2::{
    kern_return::{kern_return_t, KERN_SUCCESS},
    mach_port::mach_port_deallocate,
    mach_types::{host_t, mem_entry_name_port_t, task_t, TASK_NULL},
    message::mach_msg_type_number_t,
    port::mach_port_t,
    traps::mach_task_self,
    vm::{
        mach_make_memory_entry_64, mach_vm_deallocate, mach_vm_map, mach_vm_purgable_control,
        mach_vm_region, mach_vm_region_recurse,
    },
    vm_inherit::VM_INHERIT_NONE,
    vm_prot::{vm_prot_t, VM_PROT_EXECUTE, VM_PROT_READ, VM_PROT_WRITE},
    vm_purgable::{VM_PURGABLE_NONVOLATILE, VM_PURGABLE_SET_STATE},
    vm_region::{vm_region_submap_info_64, vm_region_submap_info_data_64_t},
    vm_statistics::{VM_FLAGS_ANYWHERE, VM_FLAGS_OVERWRITE},
    vm_types::{mach_vm_address_t, mach_vm_size_t, natural_t, vm_size_t},
};
use vm_memory::{GuestAddress, GuestMemoryMmap, GuestRegionMmap, MmapRegion};
#[cfg(target_arch = "x86_64")]
pub use x86_64::*;

#[cfg(target_arch = "aarch64")]
mod aarch64;
#[cfg(target_arch = "aarch64")]
pub use aarch64::*;

mod hypercalls;

const VM_FLAGS_4GB_CHUNK: i32 = 4;

const MACH_CHUNK_SIZE: usize = 0x8000000;

const MAP_MEM_RT: i32 = 7;
const MAP_MEM_LEDGER_TAGGED: i32 = 0x002000;
const MAP_MEM_NAMED_CREATE: i32 = 0x020000;
const MAP_MEM_VM_COPY: i32 = 0x200000;
const MAP_MEM_VM_SHARE: i32 = 0x400000;
const MAP_MEM_PURGABLE: i32 = 0x040000;

const VM_LEDGER_TAG_DEFAULT: libc::c_int = 0x00000001;

extern "C" {
    fn mach_memory_entry_ownership(
        mem_entry: mem_entry_name_port_t,
        owner: task_t,
        ledger_tag: libc::c_int,
        ledger_flags: libc::c_int,
    ) -> kern_return_t;

    fn mach_memory_object_memory_entry(
        host: host_t,
        internal: bool,
        size: vm_size_t,
        permission: vm_prot_t,
        pager: memory_object_t,
        entry_handle: *mut mach_port_t,
    ) -> kern_return_t;
}

fn get_submap_info(
    mut host_addr: mach_vm_address_t,
    mut size: mach_vm_size_t,
) -> anyhow::Result<vm_region_submap_info_64> {
    let mut nesting_level = 0;
    let mut submap_info: vm_region_submap_info_64 = Default::default();
    // VM_REGION_SUBMAP_INFO_COUNT_64
    let mut info_count = (size_of::<vm_region_submap_info_data_64_t>() / size_of::<natural_t>())
        as mach_msg_type_number_t;
    let ret = unsafe {
        mach_vm_region_recurse(
            mach_task_self(),
            &mut host_addr,
            &mut size,
            &mut nesting_level,
            &mut submap_info as *mut _ as *mut i32,
            &mut info_count,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to allocate host memory: mach_vm_region_recurse: error {}",
            ret
        ));
    }

    Ok(submap_info)
}

fn debug_madvise(host_addr: mach_vm_address_t, size: mach_vm_size_t) -> anyhow::Result<()> {
    println!("submap BEFORE: {:?}", get_submap_info(host_addr, size)?);

    // touch the memory
    unsafe {
        *(host_addr as *mut u8) = 0;
    }

    println!("submap TOUCHED: {:?}", get_submap_info(host_addr, size)?);

    // loop {
    unsafe {
        madvise(
            host_addr as *mut libc::c_void,
            16384,
            libc::MADV_FREE_REUSABLE,
        )
    };
    // }

    println!("submap MADVISED: {:?}", get_submap_info(host_addr, size)?);

    Command::new("sudo")
        .arg("footprint")
        .arg(format!("{}", unsafe { libc::getpid() }))
        .spawn()
        .unwrap()
        .wait()
        .unwrap();

    unsafe {
        std::process::exit(0);
    }
}

fn vm_allocate(mut size: mach_vm_size_t) -> anyhow::Result<*mut c_void> {
    // reserve contiguous address space, and hold onto it to prevent races until we're done mapping everything
    // this is ONLY for reserving address space; we never actually use this mapping
    let mut host_addr: mach_vm_address_t = 0;
    let ret = unsafe {
        mach_vm_map(
            mach_task_self(),
            &mut host_addr,
            size,
            0,
            // VM_FLAGS_ANYWHERE | VM_FLAGS_PURGABLE | VM_MAKE_TAG(250) as i32,
            VM_FLAGS_ANYWHERE | VM_MAKE_TAG(250) as i32,
            0,
            0,
            0,
            // we don't actually use this mapping, so fail loudly if something tries to use it
            VM_PROT_READ | VM_PROT_WRITE,
            VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE,
            // safe: we won't fork while mapping, and child won't be in the middle of this mapping code
            VM_INHERIT_NONE,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to allocate host memory: error {}",
            ret
        ));
    }

    // on failure, deallocate all chunks
    let map_guard = scopeguard::guard((), |_| {
        unsafe { mach_vm_deallocate(mach_task_self(), host_addr, size) };
    });

    // mach_vm_map splits the requested size into 128 MiB (ANON_CHUNK_SIZE) chunks for mach pager
    // max chunk size is 4 GiB,
    let mut off = 0;
    while off < size {
        let req_entry_size = std::cmp::min(MACH_CHUNK_SIZE as mach_vm_size_t, size - off);
        let mut entry_size = req_entry_size;
        let mut entry_addr = host_addr + off;

        println!(
            "VM copy @ entry_addr={:x} size={:x}",
            entry_addr, entry_size
        );
        let mut entry_port: mach_port_t = 0;
        let ret = unsafe {
            mach_make_memory_entry_64(
                mach_task_self(),
                &mut entry_size,
                entry_addr,
                MAP_MEM_NAMED_CREATE
                    // | MAP_MEM_LEDGER_TAGGED
                    | VM_PROT_READ
                    | VM_PROT_WRITE
                    | VM_PROT_EXECUTE,
                // MAP_MEM_VM_COPY | MAP_MEM_RT,
                &mut entry_port,
                0,
            )
            // mach_memory_object_memory_entry(
            //     mach_host_self(),
            //     true,
            //     entry_size as vm_size_t,
            //     VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE | VM_FLAGS_PURGABLE,
            //     0,
            //     &mut entry_port,
            // )
        };
        if ret != KERN_SUCCESS {
            return Err(anyhow::anyhow!(
                "failed to allocate host memory: mach_make_memory_entry_64: error {}",
                ret
            ));
        }

        // named entry no longer needed after mapping
        let entry_port = scopeguard::guard(entry_port, |port| {
            // unsafe { mach_port_deallocate(mach_task_self(), port) };
        });
        println!("entry_port={:?}", entry_port);

        // validate entry
        if entry_size != req_entry_size {
            return Err(anyhow::anyhow!(
                "failed to allocate host memory: requested size {} != actual size {}",
                size,
                entry_size
            ));
        }

        // let ret = unsafe {
        //     mach_memory_entry_ownership(*entry_port, TASK_NULL, VM_LEDGER_TAG_DEFAULT, 0)
        // };
        // if ret != KERN_SUCCESS {
        //     return Err(anyhow::anyhow!(
        //         "failed to allocate host memory: mach_memory_entry_ownership: error {}",
        //         ret
        //     ));
        // }

        // map it
        let ret = unsafe {
            mach_vm_map(
                mach_task_self(),
                &mut entry_addr,
                entry_size,
                0,
                VM_FLAGS_OVERWRITE | VM_MAKE_TAG(250) as i32,
                *entry_port,
                0,
                0,
                VM_PROT_READ | VM_PROT_WRITE,
                VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE,
                VM_INHERIT_NONE,
            )
            // mach_vm_region(target_task, address, size, flavor, info, infoCnt, object_name)
        };
        if ret != KERN_SUCCESS {
            return Err(anyhow::anyhow!(
                "failed to allocate host memory: mach_vm_map: error {}",
                ret
            ));
        }

        off += entry_size;
    }

    // let req_entry_size = size;
    // let mut entry_size = req_entry_size;
    // let entry_addr = host_addr;
    // let mut entry_port: mach_port_t = 0;
    // let ret = unsafe {
    //     mach_make_memory_entry_64(
    //         mach_task_self(),
    //         &mut entry_size,
    //         entry_addr,
    //         // MAP_MEM_LEDGER_TAGGED | MAP_MEM_NAMED_CREATE | MAP_MEM_RT,
    //         MAP_MEM_VM_COPY | MAP_MEM_RT,
    //         &mut entry_port,
    //         0,
    //     )
    // };
    // if ret != KERN_SUCCESS {
    //     return Err(anyhow::anyhow!(
    //         "failed to allocate host memory: mach_make_memory_entry_64: error {}",
    //         ret
    //     ));
    // }

    // // named entry no longer needed after mapping
    // let entry_port = scopeguard::guard(entry_port, |port| {
    //     unsafe { mach_port_deallocate(mach_task_self(), port) };
    // });
    // println!("entry_port={:?}", entry_port);

    // // validate entry
    // if entry_size != req_entry_size {
    //     return Err(anyhow::anyhow!(
    //         "failed to allocate host memory: requested size {} != actual size {}",
    //         size,
    //         entry_size
    //     ));
    // }

    // let mut state = VM_PURGABLE_NONVOLATILE;
    // let ret = unsafe {
    //     mach_vm_purgable_control(
    //         mach_task_self(),
    //         host_addr,
    //         VM_PURGABLE_SET_STATE,
    //         &mut state,
    //     )
    // };
    // if ret != KERN_SUCCESS {
    //     return Err(anyhow::anyhow!(
    //         "failed to allocate host memory: mach_vm_purgable_control: error {}",
    //         ret
    //     ));
    // }

    // let ret = unsafe { mach_memory_entry_ownership(*entry_port, 0, -1, 0) };
    // if ret != KERN_SUCCESS {
    //     return Err(anyhow::anyhow!(
    //         "failed to allocate host memory: mach_memory_entry_ownership: error {}",
    //         ret
    //     ));
    // }

    // we've replaced all mach chunks, so no longer need to deallocate reserved space
    // (all chunks are now from mach_make_memory_entry_64)
    std::mem::forget(map_guard);

    // debug_madvise(host_addr, size)?;

    Ok(host_addr as *mut c_void)
}

// on macOS, use the HVF API to allocate guest memory. it seems to use mach APIs
// standard mmap causes 2x overaccounting in Activity Monitor's "Memory" tab
pub fn allocate_guest_memory(ranges: &[(GuestAddress, usize)]) -> anyhow::Result<GuestMemoryMmap> {
    let regions = ranges
        .iter()
        .map(|(guest_base, size)| {
            // let host_addr = unsafe {
            //     libc::mmap(
            //         std::ptr::null_mut(),
            //         *size,
            //         libc::PROT_READ | libc::PROT_WRITE,
            //         libc::MAP_ANONYMOUS | libc::MAP_SHARED,
            //         // tag for easy identification in vmmap
            //         libc::VM_MAKE_TAG(250) as i32,
            //         0,
            //     )
            // };
            // if host_addr == libc::MAP_FAILED {
            //     return Err(anyhow::anyhow!(
            //         "failed to allocate host memory: {}",
            //         Errno::last()
            //     ));
            // }

            let host_addr = vm_allocate(*size as mach_vm_size_t)?;

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
