use std::{mem::size_of, sync::Arc};

use anyhow::anyhow;
use bitfield::bitfield;
use vm_memory::{Address, ByteValued, GuestAddress, GuestMemoryMmap};

use crate::{hvc::HvcDevice, virtio::{
    descriptor_utils::{Reader, Writer},
    fs::server::{HostContext, MAX_PAGES},
}};
use utils::memory::GuestMemoryExt;

use super::{macos::passthrough::PassthroughFs, server::Server};

#[repr(C)]
#[derive(Debug, Copy, Clone)]
struct OrbvmFuseArg {
    addr: GuestAddress,
    len: u64,
}

unsafe impl ByteValued for OrbvmFuseArg {}

impl From<&OrbvmFuseArg> for (GuestAddress, usize) {
    fn from(desc: &OrbvmFuseArg) -> (GuestAddress, usize) {
        (desc.addr, desc.len as usize)
    }
}

#[repr(C)]
#[derive(Debug, Copy, Clone)]
struct OrbvmArgs {
    in_numargs: u32,
    out_numargs: u32,
    in_pages: u32,
    out_pages: u32,
    in_args: [OrbvmFuseArg; 4],
    out_args: [OrbvmFuseArg; 3],
}

unsafe impl ByteValued for OrbvmArgs {}

bitfield! {
    #[derive(Copy, Clone)]
    #[repr(transparent)]
    struct FsDesc(u64);
    impl Debug;

    phys_addr, _: 47, 0;
    len, _: 63, 48;
}

unsafe impl ByteValued for FsDesc {}

impl From<&FsDesc> for (GuestAddress, usize) {
    fn from(desc: &FsDesc) -> (GuestAddress, usize) {
        (GuestAddress(desc.phys_addr()), desc.len() as usize)
    }
}

pub struct FsHvcDevice {
    mem: GuestMemoryMmap,
    server: Arc<Server<PassthroughFs>>,
}

impl FsHvcDevice {
    pub(crate) fn new(mem: GuestMemoryMmap, server: Arc<Server<PassthroughFs>>) -> Self {
        Self { mem, server }
    }

    pub fn handle_hvc(&self, args_addr: GuestAddress) -> anyhow::Result<i64> {
        // read args struct
        let args: OrbvmArgs = self.mem.read_obj_fast(args_addr)?;

        if args.in_numargs as usize > args.in_args.len() {
            return Err(anyhow!("too many input args"));
        }
        if args.out_numargs as usize > args.out_args.len() {
            return Err(anyhow!("too many output args"));
        }
        if args.in_pages > MAX_PAGES {
            return Err(anyhow!("too many input pages"));
        }
        if args.out_pages > MAX_PAGES {
            return Err(anyhow!("too many output pages"));
        }
        if args.in_pages != 0 && args.out_pages != 0 {
            return Err(anyhow!("cannot have both input and output pages"));
        }

        // read pages
        let pages_addr = args_addr.unchecked_add(size_of::<OrbvmArgs>() as u64);

        let reader = if args.in_pages == 0 {
            Reader::new_from_iter(
                &self.mem,
                args.in_args[..args.in_numargs as usize]
                    .iter()
                    .map(Into::into),
            )?
        } else {
            let in_pages: &[FsDesc] =
                unsafe { self.mem.get_obj_slice(pages_addr, args.in_pages as usize)? };
            Reader::new_from_iter(
                &self.mem,
                args.in_args[..args.in_numargs as usize]
                    .iter()
                    .map(Into::into)
                    .chain(in_pages.iter().map(Into::into)),
            )?
        };

        let writer = if args.out_pages == 0 {
            Writer::new_from_iter(
                &self.mem,
                args.out_args[..args.out_numargs as usize]
                    .iter()
                    .map(Into::into),
            )?
        } else {
            let out_pages: &[FsDesc] = unsafe {
                self.mem
                    .get_obj_slice(pages_addr, args.out_pages as usize)?
            };
            Writer::new_from_iter(
                &self.mem,
                args.out_args[..args.out_numargs as usize]
                    .iter()
                    .map(Into::into)
                    .chain(out_pages.iter().map(Into::into)),
            )?
        };

        debug!(?args, "hvc req");

        let hctx = HostContext { is_sync_call: true };
        if let Err(e) = self.server.handle_message(hctx, reader, writer) {
            error!("error handling message: {:?}", e);
        }

        Ok(0)
    }
}

impl HvcDevice for FsHvcDevice {
    fn call_hvc(&self, args_addr: GuestAddress) -> i64 {
        match self.handle_hvc(args_addr) {
            Ok(ret) => ret,
            Err(e) => {
                error!("error handling HVC: {:?}", e);
                -1
            }
        }
    }

    fn hvc_id(&self) -> Option<usize> {
        self.server.hvc_id()
    }
}
