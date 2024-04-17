use std::{
    io::Read,
    mem::{size_of, MaybeUninit},
    sync::Arc,
};

use vm_memory::{ByteValued, GuestAddress, GuestMemoryMmap};

use crate::virtio::{
    descriptor_utils::{GuestIoSlice, Reader, Writer},
    fs::server::MAX_PAGES,
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

    pub fn handle_hvc(&self, args_ptr: usize) -> anyhow::Result<i64> {
        // read args struct
        let mut args_reader = Reader::new_from_slices(
            &self.mem,
            &[GuestIoSlice::new(
                GuestAddress(args_ptr as u64),
                size_of::<RsvmArgs>() + size_of::<GuestIoSlice>() * MAX_PAGES as usize,
            )],
            &[],
        )
        .map_err(FsError::QueueReader)
        .unwrap();
        let args: RsvmArgs = args_reader.read_obj()?;

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
        if args.in_pages != 0 {
            args_reader.read_exact(unsafe {
                std::slice::from_raw_parts_mut(
                    pages.as_mut_ptr() as *mut u8,
                    size_of::<GuestIoSlice>() * args.in_pages as usize,
                )
            })?;
            in_pages = &pages[..args.in_pages as usize];
        } else if args.out_pages != 0 {
            args_reader.read_exact(unsafe {
                std::slice::from_raw_parts_mut(
                    pages.as_mut_ptr() as *mut u8,
                    size_of::<GuestIoSlice>() * args.out_pages as usize,
                )
            })?;
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

        if let Err(e) = self.server.handle_message(reader, writer, None) {
            error!("error handling message: {:?}", e);
        }

        Ok(0)
    }
}

impl HvcDevice for FsHvcDevice {
    fn call_hvc(&self, args_ptr: usize) -> i64 {
        match self.handle_hvc(args_ptr) {
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
