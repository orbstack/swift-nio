use std::{os::fd::RawFd, sync::Arc};

use nix::errno::Errno;
use utils::{memory::GuestMemory, Mutex};

use crate::{
    legacy::Gic,
    virtio::{descriptor_utils::Iovec, Queue},
};

use super::{
    backend::{NetBackend, ReadError, WriteError},
    worker::IovecsBuffer,
    QUEUE_SIZE,
};

// both sides have the same callbacks
pub trait NetCallbacks: Send + Sync {
    fn write_packet(&self, iovs: &[Iovec], len: usize) -> nix::Result<()>;
}

pub trait HostNetCallbacks: NetCallbacks {
    fn set_guest_callbacks(&self, callbacks: Option<Arc<dyn NetCallbacks>>);
}

pub struct CallbackBackend {
    callbacks: Arc<dyn HostNetCallbacks>,
}

impl CallbackBackend {
    pub fn new(
        callbacks: Arc<dyn HostNetCallbacks>,
        queue: Queue,
        mem: GuestMemory,
        intc: Option<Arc<Gic>>,
        irq_line: Option<u32>,
    ) -> CallbackBackend {
        let guest_cb: Arc<dyn NetCallbacks> =
            Arc::new(GuestCallbacks(Mutex::new(GuestCallbacksInner {
                guest_rx_queue: queue,
                mem,
                intc,
                irq_line,
                iovecs_buf: IovecsBuffer::with_capacity(QUEUE_SIZE as usize),
            })));

        callbacks.set_guest_callbacks(Some(guest_cb.clone()));

        CallbackBackend { callbacks }
    }
}

impl NetBackend for CallbackBackend {
    // read from backend to guest
    fn read_frame(&mut self, _buf: &[Iovec]) -> Result<usize, ReadError> {
        Err(ReadError::NothingRead)
    }

    // write from guest to backend
    fn write_frame(&mut self, hdr_len: usize, mut iovs: &mut [Iovec]) -> Result<(), WriteError> {
        // skip virtio-net header
        if !iovs.is_empty() {
            #[allow(clippy::comparison_chain)]
            if iovs[0].len() == hdr_len {
                // don't leave an empty iovec
                iovs = &mut iovs[1..];
            } else if iovs[0].len() > hdr_len {
                iovs[0].advance(hdr_len);
            }
        }

        let total_len = iovs.iter().map(|iov| iov.len()).sum();
        self.callbacks
            .write_packet(iovs, total_len)
            .map_err(WriteError::Internal)?;
        Ok(())
    }

    fn raw_socket_fd(&self) -> Option<RawFd> {
        None
    }
}

impl Drop for CallbackBackend {
    fn drop(&mut self) {
        self.callbacks.set_guest_callbacks(None);
    }
}

struct GuestCallbacksInner {
    guest_rx_queue: Queue,
    mem: GuestMemory,
    intc: Option<Arc<Gic>>,
    irq_line: Option<u32>,
    iovecs_buf: IovecsBuffer,
}

impl GuestCallbacksInner {
    fn write_packet(&mut self, src_iovs: &[Iovec], len: usize) -> nix::Result<()> {
        let queue = &mut self.guest_rx_queue;
        let head = queue.pop(&self.mem).ok_or(Errno::EAGAIN)?;
        let head_index = head.index;

        let mut dest_iovs = self.iovecs_buf.clear();
        for desc in head.into_iter() {
            let vs = self
                .mem
                .get_slice(desc.addr, desc.len as usize)
                .map_err(|_| Errno::EFAULT)?;
            dest_iovs.push(Iovec::from(vs));
        }

        let written = copy_iovs(src_iovs, &mut dest_iovs);
        if written != len {
            queue.undo_pop();
            return Err(Errno::ENOBUFS);
        }
        drop(dest_iovs);

        queue
            .add_used(&self.mem, head_index, written as u32)
            .map_err(|_| Errno::EOVERFLOW)?;

        if queue.needs_notification(&self.mem).unwrap() {
            if let Some(intc) = &self.intc {
                intc.set_irq(self.irq_line.unwrap());
            }
        }

        Ok(())
    }
}

pub struct GuestCallbacks(Mutex<GuestCallbacksInner>);

impl NetCallbacks for GuestCallbacks {
    fn write_packet(&self, iovs: &[Iovec], len: usize) -> nix::Result<()> {
        self.0.lock().unwrap().write_packet(iovs, len)
    }
}

fn copy_iovs(src_iovs: &[Iovec], mut dest_iovs: &mut [Iovec]) -> usize {
    let mut copied = 0;
    for iov in src_iovs {
        let mut src_p = iov.as_ptr();
        let mut src_rem = iov.len();
        while src_rem > 0 {
            let dest_iov = match dest_iovs.first_mut() {
                Some(iov) => iov,
                None => return copied,
            };

            let dest_p = dest_iov.as_mut_ptr();
            let dest_len = dest_iov.len();
            let to_copy = std::cmp::min(src_rem, dest_len);

            unsafe {
                std::ptr::copy_nonoverlapping(src_p, dest_p, to_copy);
            }

            src_p = unsafe { src_p.add(to_copy) };
            src_rem -= to_copy;
            copied += to_copy;

            // did we fully consume the dest iov?
            if to_copy == dest_len {
                // yes: move to the next one
                dest_iovs = &mut dest_iovs[1..];
            } else {
                // no: advance the dest iov
                dest_iov.advance(to_copy);
            }
        }
    }
    copied
}
