use std::{
    mem::{size_of, MaybeUninit},
    sync::Arc,
};

use vm_memory::{Address, ByteValued, Bytes, GuestAddress, GuestMemoryMmap};

use crate::virtio::{
    descriptor_utils::{GuestIoSlice, Reader, Writer},
    fs::server::{HostContext, MAX_PAGES},
    FsError, HvcDevice,
};

use super::{macos::passthrough::PassthroughFs, server::Server};

// TODO: packed, without Rust complaining about alignment
#[repr(C)]
#[derive(Debug, Copy, Clone)]
struct RsvmArgs {
    in_numargs: u32,
    out_numargs: u32,
    in_pages: u32,
    out_pages: u32,
    in_args: [GuestIoSlice; 4],
    out_args: [GuestIoSlice; 3],
}

unsafe impl ByteValued for RsvmArgs {}

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
        let args: RsvmArgs = self.mem.read_obj(args_addr)?;

        if args.in_numargs as usize > args.in_args.len() {
            todo!();
        }
        if args.out_numargs as usize > args.out_args.len() {
            todo!();
        }

        // read pages
        let pages: MaybeUninit<[GuestIoSlice; MAX_PAGES as usize]> = MaybeUninit::uninit();
        let mut pages = unsafe { pages.assume_init() };
        let mut in_pages: &[GuestIoSlice] = &[];
        let mut out_pages: &[GuestIoSlice] = &[];
        let pages_addr = args_addr.unchecked_add(size_of::<RsvmArgs>() as u64);
        if args.in_pages != 0 {
            self.mem.read_slice(
                unsafe {
                    std::slice::from_raw_parts_mut(
                        pages.as_mut_ptr() as *mut u8,
                        size_of::<GuestIoSlice>() * args.in_pages as usize,
                    )
                },
                pages_addr,
            )?;
            in_pages = &pages[..args.in_pages as usize];
        } else if args.out_pages != 0 {
            self.mem.read_slice(
                unsafe {
                    std::slice::from_raw_parts_mut(
                        pages.as_mut_ptr() as *mut u8,
                        size_of::<GuestIoSlice>() * args.out_pages as usize,
                    )
                },
                pages_addr,
            )?;
            out_pages = &pages[..args.out_pages as usize];
        }

        debug!(
            "hvc req: {:?}  in_pages={:?}  out_pages={:?}",
            args, in_pages, out_pages
        );

        let reader = Reader::new_from_slices(
            &self.mem,
            &args.in_args[..args.in_numargs as usize],
            in_pages,
        )
        .map_err(FsError::QueueReader)
        .unwrap();
        let writer = Writer::new_from_slices(
            &self.mem,
            &args.out_args[..args.out_numargs as usize],
            out_pages,
        )
        .map_err(FsError::QueueWriter)
        .unwrap();

        let hctx = HostContext { is_sync_call: true };
        if let Err(e) = self.server.handle_message(hctx, reader, writer, None) {
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
