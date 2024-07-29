use anyhow::anyhow;
use bitfield::bitfield;
use bitflags::bitflags;
use smallvec::SmallVec;
use std::{
    mem::{size_of, MaybeUninit},
    sync::Arc,
};
use utils::hypercalls::HVC_DEVICE_BLOCK_START;
use virtio_bindings::virtio_blk::{
    VIRTIO_BLK_T_DISCARD, VIRTIO_BLK_T_IN, VIRTIO_BLK_T_OUT, VIRTIO_BLK_T_WRITE_ZEROES,
};
use vm_memory::{Address, ByteValued, Bytes, GuestAddress, GuestMemory, GuestMemoryMmap};

use crate::virtio::{descriptor_utils::Iovec, HvcDevice};

use super::{device::DiskProperties, SECTOR_SIZE};

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
    fn for_each_iovec(
        &self,
        args_addr: GuestAddress,
        mem: &GuestMemoryMmap,
        mut f: impl FnMut(usize, Iovec<'static>) -> anyhow::Result<()>,
    ) -> anyhow::Result<()> {
        if self.nr_segs as usize > MAX_SEGS {
            return Err(anyhow!("too many segments"));
        }

        let mut off = self.start_off as usize;

        // read segs
        let descs_buf: MaybeUninit<[BlkDesc; MAX_SEGS]> = MaybeUninit::uninit();
        let mut descs_buf = unsafe { descs_buf.assume_init() };
        let descs_addr = args_addr.unchecked_add(size_of::<OrbvmBlkReqHeader>() as u64);
        mem.read_slice(
            unsafe {
                std::slice::from_raw_parts_mut(
                    descs_buf.as_mut_ptr() as *mut u8,
                    size_of::<BlkDesc>() * self.nr_segs as usize,
                )
            },
            descs_addr,
        )?;
        let descs = &descs_buf[..self.nr_segs as usize];

        for desc in descs {
            let len = desc.len();
            let vs = mem.get_slice(GuestAddress(desc.phys_addr()), len)?;
            let iov = Iovec::from_static(vs);
            f(off, iov)?;
            off += len;
        }

        Ok(())
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
    index: usize,
}

impl BlockHvcDevice {
    pub(crate) fn new(mem: GuestMemoryMmap, disk: Arc<DiskProperties>, index: usize) -> Self {
        BlockHvcDevice { mem, disk, index }
    }

    fn handle_hvc(&self, args_addr: GuestAddress) -> anyhow::Result<()> {
        let hdr = self.mem.read_obj::<OrbvmBlkReqHeader>(args_addr)?;

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

            VIRTIO_BLK_T_DISCARD | VIRTIO_BLK_T_WRITE_ZEROES => {
                self.disk
                    .punch_hole(hdr.start_off, hdr.discard_len)
                    .map_err(|e| anyhow!("discard failed: {:?}", e))?;
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
