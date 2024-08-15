use anyhow::anyhow;
use bitfield::bitfield;
use bitflags::bitflags;
use hvf::{HvfVm, MemoryFlags};
use smallvec::SmallVec;
use std::collections::HashMap;
use std::{mem::size_of, sync::Arc};
use utils::memory::GuestMemoryExt;
use utils::{hypercalls::HVC_DEVICE_BLOCK_START, Mutex};
use virtio_bindings::virtio_blk::{
    VIRTIO_BLK_T_DISCARD, VIRTIO_BLK_T_FLUSH, VIRTIO_BLK_T_IN, VIRTIO_BLK_T_OUT,
    VIRTIO_BLK_T_WRITE_ZEROES,
};
use vm_memory::{Address, ByteValued, GuestAddress, GuestMemory, GuestMemoryMmap};

use crate::virtio::{descriptor_utils::Iovec, HvcDevice, VirtioShmRegion};

use super::{device::DiskProperties, SECTOR_SIZE};

const ORBVM_BLK_DAX_CHUNK_SIZE: usize = 64 * 1024 * 1024; // 64 MiB

const MAX_SEGS: usize = 256;

bitflags! {
    #[derive(Clone, Copy, Debug)]
    struct OrbvmBlkFlags: u16 {
        const PREFLUSH = 1 << 0;
        const POSTFLUSH = 1 << 1;
    }
}

#[derive(Copy, Clone)]
#[repr(C)]
struct OrbvmBlkReqHeader {
    type_: u16,
    flags: OrbvmBlkFlags,
    nr_segs: u16,
    discard_len: u64,
    start_off: u64,
}

unsafe impl ByteValued for OrbvmBlkReqHeader {}

impl OrbvmBlkReqHeader {
    fn for_each_desc(
        &self,
        args_addr: GuestAddress,
        mem: &GuestMemoryMmap,
        mut f: impl FnMut(&BlkDesc) -> anyhow::Result<()>,
    ) -> anyhow::Result<()> {
        if self.nr_segs as usize > MAX_SEGS {
            return Err(anyhow!("too many segments"));
        }

        // read segs
        let descs_addr = args_addr.unchecked_add(size_of::<OrbvmBlkReqHeader>() as u64);
        let descs: &[BlkDesc] = unsafe { mem.get_obj_slice(descs_addr, self.nr_segs as usize)? };

        for desc in descs {
            f(desc)?;
        }

        Ok(())
    }

    fn for_each_iovec(
        &self,
        args_addr: GuestAddress,
        mem: &GuestMemoryMmap,
        mut f: impl FnMut(usize, Iovec<'static>) -> anyhow::Result<()>,
    ) -> anyhow::Result<()> {
        let mut off = self.start_off as usize;
        self.for_each_desc(args_addr, mem, |desc| {
            let len = desc.len();
            let vs = mem.get_slice(GuestAddress(desc.phys_addr()), len)?;
            let iov = Iovec::from_static(vs);
            f(off, iov)?;
            off += len;
            Ok(())
        })
    }
}

bitfield! {
    #[derive(Copy, Clone)]
    #[repr(transparent)]
    struct BlkDesc(u64);
    impl Debug;

    phys_addr, _: 47, 0;
    len_sectors, _: 63, 48;
}

unsafe impl ByteValued for BlkDesc {}

impl BlkDesc {
    fn len(&self) -> usize {
        self.len_sectors() as usize * SECTOR_SIZE as usize
    }
}

pub struct BlockHvcDevice {
    mem: GuestMemoryMmap,
    disk: Arc<DiskProperties>,
    shm_region: Option<VirtioShmRegion>,
    hvf_vm: Arc<HvfVm>,
    index: usize,
    mappings: Mutex<HashMap<u64, usize>>,
}

impl BlockHvcDevice {
    pub(crate) fn new(
        mem: GuestMemoryMmap,
        disk: Arc<DiskProperties>,
        shm_region: Option<VirtioShmRegion>,
        hvf_vm: Arc<HvfVm>,
        index: usize,
    ) -> Self {
        BlockHvcDevice {
            mem,
            disk,
            shm_region,
            hvf_vm,
            index,
            mappings: Mutex::new(HashMap::new()),
        }
    }

    fn handle_hvc(&self, args_addr: GuestAddress) -> anyhow::Result<()> {
        let hdr = self.mem.read_obj_fast::<OrbvmBlkReqHeader>(args_addr)?;

        debug!(
            "block hvc: type_: {}, flags: {:?}, nr_segs: {}, start_off: {}",
            hdr.type_, hdr.flags, hdr.nr_segs, hdr.start_off
        );

        // do PREFLUSH
        if hdr.flags.contains(OrbvmBlkFlags::PREFLUSH) {
            self.disk
                .flush()
                .map_err(|e| anyhow!("preflush failed: {:?}", e))?;
        }

        match hdr.type_ as u32 {
            VIRTIO_BLK_T_IN => {
                hdr.for_each_iovec(args_addr, &self.mem, |off, iov| {
                    self.disk.read_to_iovec(off, &iov)?;
                    Ok(())
                })
                .map_err(|e| anyhow!("read failed: {:?}", e))?;
            }

            VIRTIO_BLK_T_OUT => {
                let mut iovecs = SmallVec::<[Iovec; MAX_SEGS]>::new();
                hdr.for_each_iovec(args_addr, &self.mem, |_, iov| {
                    iovecs.push(iov);
                    Ok(())
                })?;

                // can be empty due to PREFLUSH/POSTFLUSH
                if !iovecs.is_empty() {
                    self.disk
                        .write_iovecs(hdr.start_off, &iovecs)
                        .map_err(|e| anyhow!("write failed: {:?}", e))?;
                }
            }

            VIRTIO_BLK_T_FLUSH => {
                self.disk
                    .flush()
                    .map_err(|e| anyhow!("flush failed: {:?}", e))?;
            }

            VIRTIO_BLK_T_DISCARD | VIRTIO_BLK_T_WRITE_ZEROES => {
                hdr.for_each_desc(args_addr, &self.mem, |desc| {
                    let off = desc.phys_addr() * SECTOR_SIZE;
                    self.disk
                        .punch_hole(off, desc.len() as u64)
                        .map_err(|e| anyhow!("discard failed: {:?}", e))
                })?;
            }

            64 => {
                let slot_index = hdr.nr_segs as usize;
                // clamp to end, if full chunk size would exceed it
                let chunk_size = std::cmp::min(
                    ORBVM_BLK_DAX_CHUNK_SIZE,
                    self.disk.size() as usize - hdr.start_off as usize,
                );
                let host_addr = self
                    .disk
                    .get_host_addr(hdr.start_off as usize, chunk_size)?;
                let dax_region = self.shm_region.as_ref().unwrap();
                let guest_addr = dax_region
                    .guest_addr
                    .checked_add(slot_index as u64 * ORBVM_BLK_DAX_CHUNK_SIZE as u64)
                    .unwrap();

                info!(
                    "block hvc: map dax chunk: host_addr: 0x{:x}, guest_addr: 0x{:x}",
                    host_addr as u64,
                    guest_addr.raw_value()
                );
                let mut mappings = self.mappings.lock().unwrap();
                if let Some(old_chunk_size) = mappings.remove(&guest_addr.raw_value()) {
                    self.hvf_vm.unmap_memory(guest_addr, old_chunk_size)?;
                }
                unsafe {
                    self.hvf_vm.map_memory(
                        // read-only
                        host_addr as *mut u8,
                        guest_addr,
                        chunk_size,
                        MemoryFlags::READ,
                    )?
                };
                mappings.insert(guest_addr.raw_value(), chunk_size);
            }

            _ => {
                return Err(anyhow!("unsupported request type: {}", hdr.type_));
            }
        }

        // do POSTFLUSH
        if hdr.flags.contains(OrbvmBlkFlags::POSTFLUSH) {
            self.disk
                .flush()
                .map_err(|e| anyhow!("postflush failed: {:?}", e))?;
        }

        Ok(())
    }
}

impl HvcDevice for BlockHvcDevice {
    fn hvc_id(&self) -> Option<usize> {
        Some(HVC_DEVICE_BLOCK_START + self.index)
    }

    fn call_hvc(&self, args_addr: GuestAddress) -> i64 {
        match self.handle_hvc(args_addr) {
            Ok(_) => 0,
            Err(e) => {
                error!("block hvc failed: {:?}", e);
                -1
            }
        }
    }
}
