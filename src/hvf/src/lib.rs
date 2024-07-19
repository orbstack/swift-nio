#[cfg(target_arch = "x86_64")]
mod x86_64;
use std::{
    cell::Cell,
    mem::size_of,
    ops::BitAnd,
    process::Command,
    sync::{atomic::AtomicBool, Condvar, Mutex, OnceLock},
};

use anyhow::anyhow;
use libc::{
    c_void, getpid, mach_host_self, madvise, memory_object_t, proc_pidinfo, sysctlbyname,
    VM_FLAGS_PURGABLE, VM_MAKE_TAG,
};
use mach2::{
    kern_return::{kern_return_t, KERN_SUCCESS},
    mach_port::{
        mach_port_allocate, mach_port_deallocate, mach_port_insert_right, mach_port_mod_refs,
    },
    mach_types::{host_t, mem_entry_name_port_t, task_t, TASK_NULL},
    message::{
        mach_msg, mach_msg_body_t, mach_msg_header_t, mach_msg_port_descriptor_t, mach_msg_send,
        mach_msg_trailer_t, mach_msg_type_number_t, MACH_MSGH_BITS, MACH_MSGH_BITS_COMPLEX,
        MACH_MSG_PORT_DESCRIPTOR, MACH_MSG_TYPE_COPY_SEND, MACH_MSG_TYPE_MAKE_SEND, MACH_RCV_MSG,
    },
    port::{mach_port_t, MACH_PORT_NULL, MACH_PORT_RIGHT_RECEIVE, MACH_PORT_RIGHT_SEND},
    task::{task_get_special_port, task_special_port_t, TASK_BOOTSTRAP_PORT},
    traps::mach_task_self,
    vm::{
        mach_make_memory_entry_64, mach_vm_allocate, mach_vm_deallocate, mach_vm_map,
        mach_vm_purgable_control, mach_vm_region, mach_vm_region_recurse, mach_vm_remap,
    },
    vm_inherit::VM_INHERIT_NONE,
    vm_prot::{vm_prot_t, VM_PROT_EXECUTE, VM_PROT_READ, VM_PROT_WRITE},
    vm_purgable::{VM_PURGABLE_EMPTY, VM_PURGABLE_NONVOLATILE, VM_PURGABLE_SET_STATE},
    vm_region::{vm_region_submap_info_64, vm_region_submap_info_data_64_t},
    vm_statistics::{VM_FLAGS_ANYWHERE, VM_FLAGS_FIXED, VM_FLAGS_OVERWRITE},
    vm_types::{mach_vm_address_t, mach_vm_size_t, natural_t, vm_size_t},
};
use nix::errno::Errno;
use once_cell::sync::Lazy;
use tracing::error;
use vm_memory::{
    Address, GuestAddress, GuestMemory, GuestMemoryMmap, GuestMemoryRegion, GuestRegionMmap,
    MmapRegion,
};
#[cfg(target_arch = "x86_64")]
pub use x86_64::*;

#[cfg(target_arch = "aarch64")]
mod aarch64;
#[cfg(target_arch = "aarch64")]
pub use aarch64::*;

const VM_FLAGS_4GB_CHUNK: i32 = 4;

const PAGE_SIZE: usize = 16384;
const MACH_CHUNK_SIZE: usize = 2097152;

const MAP_MEM_RT: i32 = 7;
const MAP_MEM_LEDGER_TAGGED: i32 = 0x002000;
const MAP_MEM_NAMED_CREATE: i32 = 0x020000;
const MAP_MEM_VM_COPY: i32 = 0x200000;
const MAP_MEM_VM_SHARE: i32 = 0x400000;
const MAP_MEM_PURGABLE: i32 = 0x040000;

const VM_LEDGER_TAG_DEFAULT: libc::c_int = 0x00000001;

#[derive(Default, Debug)]
#[repr(C)]
struct MachMsgWithTrailer {
    header: mach_msg_header_t,
    body: mach_msg_body_t,
    port: mach_msg_port_descriptor_t,
    trailer: mach_msg_trailer_t,
}

#[derive(Default, Debug)]
#[repr(C)]
struct MachMsg {
    header: mach_msg_header_t,
    body: mach_msg_body_t,
    port: mach_msg_port_descriptor_t,
}

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

    fn task_set_special_port(
        task: task_t,
        which_port: task_special_port_t,
        special_port: mach_port_t,
    ) -> kern_return_t;
}

#[derive(Default, Debug, Clone, Copy)]
#[repr(C)]
struct proc_regioninfo {
    pri_protection: u32,
    pri_max_protection: u32,
    pri_inheritance: u32,
    pri_flags: u32, /* shared, external pager, is submap */
    pri_offset: u64,
    pri_behavior: u32,
    pri_user_wired_count: u32,
    pri_user_tag: u32,
    pri_pages_resident: u32,
    pri_pages_shared_now_private: u32,
    pri_pages_swapped_out: u32,
    pri_pages_dirtied: u32,
    pri_ref_count: u32,
    pri_shadow_depth: u32,
    pri_share_mode: u32,
    pri_private_pages_resident: u32,
    pri_shared_pages_resident: u32,
    pri_obj_id: u32,
    pri_depth: u32,
    pri_address: u64,
    pri_size: u64,
}

const PROC_PIDREGIONINFO: i32 = 7;

extern "C" {
    fn task_self_region_footprint_set(enable: bool);
}

fn get_submap_info(
    mut host_addr: mach_vm_address_t,
    mut size: mach_vm_size_t,
) -> anyhow::Result<proc_regioninfo> {
    let mut info: proc_regioninfo = Default::default();
    let mut old_self_region_footprint: u32 = 0;
    let mut len = size_of::<u32>();
    let mut new_self_region_footprint: u32 = 1;
    let ret = unsafe {
        sysctlbyname(
            c"vm.self_region_footprint".as_ptr(),
            &mut old_self_region_footprint as *mut _ as *mut _,
            &mut len,
            &mut new_self_region_footprint as *mut _ as *mut _,
            size_of::<u32>() as _,
        )
    };
    Errno::result(ret)?;
    let ret = unsafe {
        proc_pidinfo(
            getpid(),
            PROC_PIDREGIONINFO,
            host_addr,
            &mut info as *mut _ as *mut _,
            size_of::<proc_regioninfo>() as i32,
        )
    };
    Errno::result(ret)?;

    Ok(info)
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

static VM_ALLOCATION: OnceLock<VmAllocation> = OnceLock::new();

struct VmAllocation {
    regions: Vec<VmRegion>,
}

unsafe impl Sync for VmAllocation {}
unsafe impl Send for VmAllocation {}

struct VmRegion {
    host_addr: *mut c_void,
    size: usize,
}

pub fn on_vm_park() -> anyhow::Result<()> {
    // let guard = VM_ALLOCATION.lock().unwrap();
    // let alloc_info = guard.as_ref().unwrap();
    // for region in &alloc_info.regions {
    //     println!("unmap: {:p} ({:x})", region.host_addr, region.size);
    //     let ret = unsafe {
    //         mach_vm_deallocate(mach_task_self(), region.host_addr as _, region.size as _)
    //     };
    //     if ret != KERN_SUCCESS {
    //         panic!("failed to deallocate host memory: error {}", ret);
    //     }
    // }

    Ok(())
}

pub fn on_vm_unpark() -> anyhow::Result<()> {
    // remap_all()?;
    Ok(())
}

pub fn remap_all() -> anyhow::Result<()> {
    // TODO: only remap if we've racked up enoguh double accounting. this accounts for most of the cost of unparking. we also need that to do periodic remap if no balloon action happens
    let _span = tracing::info_span!("remap_user").entered();

    let alloc_info = VM_ALLOCATION.get().unwrap();
    for region in &alloc_info.regions {
        // println!("remap: {:p} ({:x})", region.host_addr, region.size);
        // println!(
        //     "submap before: {:?}",
        //     get_submap_info(region.host_addr as _, region.size as _).unwrap(),
        // );

        // clear double accounting
        let mut target_address = region.host_addr as mach_vm_address_t;
        let mut cur_prot = VM_PROT_READ | VM_PROT_WRITE;
        let mut max_prot = VM_PROT_READ | VM_PROT_WRITE | VM_PROT_EXECUTE;
        let ret = unsafe {
            mach_vm_remap(
                mach_task_self(),
                &mut target_address,
                region.size as mach_vm_size_t,
                0,
                VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE,
                mach_task_self(),
                region.host_addr as mach_vm_address_t,
                0,
                &mut cur_prot,
                &mut max_prot,
                VM_INHERIT_NONE,
            )
        };
        if ret != KERN_SUCCESS {
            panic!("failed to remap host memory: error {}", ret);
        }
        // println!(
        //     "submap after: {:?}",
        //     get_submap_info(region.host_addr as _, region.size as _).unwrap(),
        // );
    }

    Ok(())
}

fn send_port(remote_port: mach_port_t, port: mach_port_t) -> anyhow::Result<()> {
    let mut msg = MachMsg {
        header: mach_msg_header_t {
            msgh_size: size_of::<MachMsg>() as u32,
            msgh_remote_port: remote_port,
            msgh_local_port: MACH_PORT_NULL,
            msgh_bits: MACH_MSGH_BITS(MACH_MSG_TYPE_COPY_SEND, 0) | MACH_MSGH_BITS_COMPLEX,
            ..Default::default()
        },
        body: mach_msg_body_t {
            msgh_descriptor_count: 1,
        },
        port: mach_msg_port_descriptor_t {
            name: port,
            disposition: MACH_MSG_TYPE_COPY_SEND as u8,
            type_: MACH_MSG_PORT_DESCRIPTOR as u8,
            ..Default::default()
        },
    };
    println!("sending port {:?} to {:?}", port, remote_port);

    let ret = unsafe { mach_msg_send(&mut msg.header) };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to send port: mach_msg_send: error {}",
            ret
        ));
    }

    Ok(())
}

fn recv_port(recv_port: mach_port_t) -> anyhow::Result<mach_port_t> {
    let mut msg = MachMsgWithTrailer::default();

    let ret = unsafe {
        mach_msg(
            &mut msg.header,
            MACH_RCV_MSG,
            0,
            size_of::<MachMsgWithTrailer>() as u32,
            recv_port,
            0,
            0,
        )
    };
    if ret != KERN_SUCCESS {
        println!("failed to recv port: mach_msg: error {}", ret);
        panic!();
    }

    Ok(msg.port.name)
}

fn fork_and_disown(entry_port: mach_port_t) -> anyhow::Result<()> {
    // get old bootstrap port
    let mut old_bootstrap_port: mach_port_t = 0;
    let ret = unsafe {
        task_get_special_port(
            mach_task_self(),
            TASK_BOOTSTRAP_PORT,
            &mut old_bootstrap_port,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to fork and disown: task_get_special_port: error {}",
            ret
        ));
    }

    println!("old bootstrap port = {:?}", old_bootstrap_port);

    // allocate new port for IPC
    let mut ipc_port: mach_port_t = 0;
    let ret =
        unsafe { mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &mut ipc_port) };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to fork and disown: mach_port_allocate: error {}",
            ret
        ));
    }

    println!("ipc port = {:?}", ipc_port);

    // give it a send right
    let ret = unsafe {
        mach_port_insert_right(
            mach_task_self(),
            ipc_port,
            ipc_port,
            MACH_MSG_TYPE_MAKE_SEND,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to fork and disown: mach_port_insert_right: error {}",
            ret
        ));
    }

    // set new bootstrap port
    let ret = unsafe { task_set_special_port(mach_task_self(), TASK_BOOTSTRAP_PORT, ipc_port) };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to fork and disown: task_set_special_port: error {}",
            ret
        ));
    }

    // read it back
    let mut read_back_port: mach_port_t = 0;
    let ret = unsafe {
        task_get_special_port(mach_task_self(), TASK_BOOTSTRAP_PORT, &mut read_back_port)
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to fork and disown: task_get_special_port: error {}",
            ret
        ));
    }

    println!("read back port = {:?}", read_back_port);

    // fork
    let ret = unsafe { libc::fork() };
    if ret == -1 {
        return Err(anyhow::anyhow!(
            "failed to fork: {}",
            std::io::Error::last_os_error()
        ));
    }

    // parent process
    if ret > 0 {
        // restore old bootstrap port
        let ret = unsafe {
            task_set_special_port(mach_task_self(), TASK_BOOTSTRAP_PORT, old_bootstrap_port)
        };
        if ret != KERN_SUCCESS {
            return Err(anyhow::anyhow!(
                "failed to fork and disown: task_set_special_port: error {}",
                ret
            ));
        }
        // recv secondary IPC port from child
        let secondary_ipc_port = recv_port(ipc_port).unwrap();

        println!("received port from child: {:?}", secondary_ipc_port);

        // inc send right on entry port
        let ret =
            unsafe { mach_port_mod_refs(mach_task_self(), entry_port, MACH_PORT_RIGHT_SEND, 1) };
        if ret != KERN_SUCCESS {
            return Err(anyhow::anyhow!(
                "failed to fork and disown: mach_port_mod_refs: error {}",
                ret
            ));
        }

        // send mem port to child
        send_port(secondary_ipc_port, entry_port)?;

        return Ok(());
    }

    // child process
    println!("in child!");

    // get bootstrap port
    println!("getting bootstrap port...");
    let mut ipc_port: mach_port_t = 0;
    let ret =
        unsafe { task_get_special_port(mach_task_self(), TASK_BOOTSTRAP_PORT, &mut ipc_port) };
    if ret != KERN_SUCCESS {
        println!(
            "[child] failed to get bootstrap port: task_get_special_port: error {}",
            ret
        );
        panic!();
    }

    // allocate a port to receive the memory entry port
    println!("[child] allocating port...");
    let mut child_port: mach_port_t = 0;
    let ret =
        unsafe { mach_port_allocate(mach_task_self(), MACH_PORT_RIGHT_RECEIVE, &mut child_port) };
    if ret != KERN_SUCCESS {
        println!("[child] failed to allocate port: error {}", ret);
        panic!();
    }

    // give it a send right
    println!("[child] inserting right...");
    let ret = unsafe {
        mach_port_insert_right(
            mach_task_self(),
            child_port,
            child_port,
            MACH_MSG_TYPE_MAKE_SEND,
        )
    };
    if ret != KERN_SUCCESS {
        println!("[child] failed to insert right: error {}", ret);
        panic!();
    }

    println!("[child] child port = {:?}", child_port);

    // send this port to the parent, via bootstrap_port
    send_port(ipc_port, child_port)?;

    println!("sent port to parent; waiting for entry port...");

    // recv entry port
    let child_entry_port = recv_port(child_port)?;

    println!("[child] port = {:?}", child_entry_port);

    // take ownership of entry port
    let ret = unsafe {
        mach_memory_entry_ownership(child_entry_port, mach_task_self(), VM_LEDGER_TAG_DEFAULT, 0)
    };
    if ret != KERN_SUCCESS {
        println!(
            "[child] failed to take ownership of entry port: error {}",
            ret
        );
        panic!();
    }

    // hang forever
    loop {
        unsafe {
            libc::sleep(1);
        }
    }
}

fn new_chunks_at(host_addr: *mut c_void, size: usize) -> anyhow::Result<()> {
    let mut off: mach_vm_size_t = 0;
    while off < size as mach_vm_size_t {
        let entry_size = std::cmp::min(
            MACH_CHUNK_SIZE as mach_vm_size_t,
            size as mach_vm_size_t - off,
        );
        let mut entry_addr = host_addr as mach_vm_address_t + off;

        let ret = unsafe {
            mach_vm_allocate(
                mach_task_self(),
                &mut entry_addr,
                entry_size,
                VM_FLAGS_FIXED | VM_FLAGS_OVERWRITE | VM_FLAGS_PURGABLE | VM_MAKE_TAG(250) as i32,
            )
        };
        if ret != KERN_SUCCESS {
            return Err(anyhow::anyhow!(
                "failed to allocate host memory: mach_vm_allocate: error {}",
                ret
            ));
        }

        off += entry_size;
    }

    Ok(())
}

static BOUNCE_BUFFER: Lazy<Mutex<Vec<u8>>> = Lazy::new(|| vec![0; MACH_CHUNK_SIZE].into());

fn purge_chunk(entry_addr: mach_vm_address_t) -> anyhow::Result<()> {
    let mut state = VM_PURGABLE_EMPTY;
    let ret = unsafe {
        mach_vm_purgable_control(
            mach_task_self(),
            entry_addr,
            VM_PURGABLE_SET_STATE,
            &mut state,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to deallocate host memory: mach_vm_purgable_control: error {}",
            ret
        ));
    }

    state = VM_PURGABLE_NONVOLATILE;
    let ret = unsafe {
        mach_vm_purgable_control(
            mach_task_self(),
            entry_addr,
            VM_PURGABLE_SET_STATE,
            &mut state,
        )
    };
    if ret != KERN_SUCCESS {
        return Err(anyhow::anyhow!(
            "failed to deallocate host memory: mach_vm_purgable_control: error {}",
            ret
        ));
    }

    Ok(())
}

#[derive(Debug, Clone, Copy)]
struct Interval<N: Copy + Ord> {
    start_incl: N,
    end_excl: N,
}

impl<N: Copy + Ord> Interval<N> {
    pub fn new(start_incl: N, end_excl: N) -> Self {
        Self {
            start_incl,
            end_excl,
        }
    }
}

enum Intersection<N: Copy + Ord> {
    None,
    Partial(Interval<N>),
    Full,
}

impl<N: Copy + Ord> BitAnd for Interval<N> {
    type Output = Intersection<N>;

    fn bitand(self, other: Self) -> Self::Output {
        // no intersection?
        if self.end_excl <= other.start_incl || self.start_incl >= other.end_excl {
            return Intersection::None;
        }

        // full intersection?
        if self.start_incl <= other.start_incl && self.end_excl >= other.end_excl {
            return Intersection::Full;
        }

        // partial intersection
        let start_incl = std::cmp::max(self.start_incl, other.start_incl);
        let end_excl = std::cmp::min(self.end_excl, other.end_excl);
        Intersection::Partial(Interval::new(start_incl, end_excl))
    }
}

static BALLOON_CONDVAR: Lazy<(Mutex<bool>, Condvar)> =
    Lazy::new(|| (Mutex::new(false), Condvar::new()));

pub fn wait_for_balloon() {
    let (lock, cvar) = &*BALLOON_CONDVAR;
    let mut guard = lock.lock().unwrap();
    while *guard {
        guard = cvar.wait(guard).unwrap();
    }
}

pub fn set_balloon(in_balloon: bool) {
    let (lock, cvar) = &*BALLOON_CONDVAR;
    let mut guard = lock.lock().unwrap();
    *guard = in_balloon;
    cvar.notify_all();
}

pub unsafe fn free_range(
    guest_addr: GuestAddress,
    host_addr: *mut c_void,
    size: usize,
) -> anyhow::Result<()> {
    // let _span = tracing::info_span!("free_range", size = size).entered();
    // start and end must be page-aligned
    if host_addr as usize % PAGE_SIZE != 0 {
        return Err(anyhow!(
            "guest address must be page-aligned: {:x}",
            guest_addr.raw_value()
        ));
    }
    if size % PAGE_SIZE != 0 {
        return Err(anyhow!("size must be page-aligned: {}", size));
    }

    // madvise on host address
    let ret = madvise(host_addr, size, libc::MADV_FREE_REUSABLE);
    Errno::result(ret).map_err(|e| anyhow!("failed to madvise: {}", e))?;

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
    // let map_guard = scopeguard::guard((), |_| {
    //     unsafe { mach_vm_deallocate(mach_task_self(), host_addr, size) };
    // });

    // // mach_vm_map splits the requested size into 128 MiB (ANON_CHUNK_SIZE) chunks for mach pager
    // // max chunk size is 4 GiB,
    // let mut regions = Vec::new();
    // new_chunks_at(host_addr as *mut c_void, size as usize)?;

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
    // std::mem::forget(map_guard);

    // debug_madvise(host_addr, size)?;

    // TODO: on x86 this can be multiple regions, just not shm
    let alloc_info = VmAllocation {
        regions: vec![VmRegion {
            host_addr: host_addr as *mut c_void,
            size: size as usize,
        }],
    };

    VM_ALLOCATION.get_or_init(|| {
        // spawn thread to periodically remap and fix double accounting
        std::thread::spawn(|| loop {
            std::thread::sleep(std::time::Duration::from_secs(30));
            remap_all().unwrap();
        });

        alloc_info
    });

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
