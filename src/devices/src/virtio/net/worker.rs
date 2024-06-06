use crate::legacy::Gic;
use crate::virtio::net::gvproxy::Gvproxy;
use crate::virtio::net::passt::Passt;
use crate::virtio::net::{MAX_BUFFER_SIZE, QUEUE_SIZE, RX_INDEX, TX_INDEX};
use crate::virtio::{Queue, VIRTIO_MMIO_INT_VRING};
use crate::Error as DeviceError;

use super::backend::{NetBackend, ReadError, WriteError};
use super::device::{
    FrontendError, NetSignalChannel, NetSignalMask, RxError, TxError, VirtioNetBackend,
    NET_QUEUE_SIGS,
};
use super::dgram::Dgram;

use std::sync::atomic::AtomicUsize;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::{cmp, mem, result};
use utils::Mutex;
use virtio_bindings::virtio_net::virtio_net_hdr_v1;
use vm_memory::{Bytes, GuestAddress, GuestMemoryMmap};

use gruel::{MioChannelExt, OnceMioWaker};

fn vnet_hdr_len() -> usize {
    mem::size_of::<virtio_net_hdr_v1>()
}

pub struct NetWorker {
    signals: Arc<NetSignalChannel>,
    queues: Vec<Queue>,
    interrupt_status: Arc<AtomicUsize>,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,

    mem: GuestMemoryMmap,
    backend: Box<dyn NetBackend + Send>,

    rx_frame_buf: [u8; MAX_BUFFER_SIZE],
    rx_frame_buf_len: usize,
    rx_has_deferred_frame: bool,

    tx_iovec: Vec<(GuestAddress, usize)>,
    tx_frame_buf: [u8; MAX_BUFFER_SIZE],
    tx_frame_len: usize,
}

impl NetWorker {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        signals: Arc<NetSignalChannel>,
        queues: Vec<Queue>,
        interrupt_status: Arc<AtomicUsize>,
        intc: Option<Arc<Mutex<Gic>>>,
        irq_line: Option<u32>,
        mem: GuestMemoryMmap,
        cfg_backend: VirtioNetBackend,
    ) -> Self {
        let backend = match cfg_backend {
            VirtioNetBackend::Passt(fd) => Box::new(Passt::new(fd)) as Box<dyn NetBackend + Send>,
            VirtioNetBackend::Gvproxy(path) => {
                Box::new(Gvproxy::new(path).unwrap()) as Box<dyn NetBackend + Send>
            }
            VirtioNetBackend::Dgram(fd) => {
                Box::new(Dgram::new(fd).unwrap()) as Box<dyn NetBackend + Send>
            }
        };

        Self {
            signals,
            queues,
            interrupt_status,
            intc,
            irq_line,

            mem,
            backend,

            rx_frame_buf: [0u8; MAX_BUFFER_SIZE],
            rx_frame_buf_len: 0,
            rx_has_deferred_frame: false,

            tx_frame_buf: [0u8; MAX_BUFFER_SIZE],
            tx_frame_len: 0,
            tx_iovec: Vec::with_capacity(QUEUE_SIZE as usize),
        }
    }

    pub fn run(self) -> JoinHandle<()> {
        thread::Builder::new()
            .name("net worker".to_string())
            .spawn(|| self.work())
            .expect("failed to spawn thread")
    }

    fn work(mut self) {
        // Setup epoll
        let mut poll = mio::Poll::new().unwrap();
        let mut events = mio::Events::with_capacity(32);

        let backend_socket_token = mio::Token(0);
        let backend_socket = self.backend.raw_socket_fd();
        poll.registry()
            .register(
                &mut mio::unix::SourceFd(&backend_socket),
                backend_socket_token,
                mio::Interest::READABLE | mio::Interest::WRITABLE,
            )
            .unwrap();

        let waker_token = mio::Token(1);
        let waker = mio::Waker::new(poll.registry(), waker_token).unwrap();
        self.signals
            .waker_state::<OnceMioWaker>()
            .set_waker(Arc::new(waker));

        let handled_mask = NetSignalMask::SHUTDOWN_WORKER
            | NET_QUEUE_SIGS.get(RX_INDEX)
            | NET_QUEUE_SIGS.get(TX_INDEX);

        // Start worker loop
        // TODO: GRUEL - Ensure that the assert side of this routine fulfills its side of the queue
        //  protocol. This needs to be done for FS as well
        loop {
            // Wait for epoll events
            if let Err(err) = self
                .signals
                .wait_on_poll(handled_mask, &mut poll, &mut events, None)
            {
                debug!("vsock: failed to consume muxer epoll event: {err}");
            }

            // Handle signals
            let taken = self.signals.take(handled_mask);

            if taken.intersects(NetSignalMask::SHUTDOWN_WORKER) {
                return;
            }

            if taken.intersects(NET_QUEUE_SIGS.get(RX_INDEX)) {
                self.process_rx_queue_event();
            }

            if taken.intersects(NET_QUEUE_SIGS.get(RX_INDEX)) {
                self.process_tx_queue_event();
            }

            // Handle epoll events
            for event in events.iter() {
                if event.token() == waker_token {
                    continue;
                }

                debug_assert_eq!(event.token(), backend_socket_token);

                if event.is_read_closed() || event.is_write_closed() {
                    tracing::error!("Got {event:?} on backend fd, virtio-net will stop working");
                } else {
                    if event.is_readable() {
                        self.process_backend_socket_readable()
                    }

                    if event.is_writable() {
                        self.process_backend_socket_writeable()
                    }
                }
            }
        }
    }

    pub(crate) fn process_rx_queue_event(&mut self) {
        if let Err(e) = self.queues[RX_INDEX].disable_notification(&self.mem) {
            error!("error disabling queue notifications: {:?}", e);
        }

        if let Err(e) = self.process_rx() {
            tracing::error!("Failed to process rx: {e:?} (triggered by queue event)")
        };

        if let Err(e) = self.queues[RX_INDEX].enable_notification(&self.mem) {
            error!("error disabling queue notifications: {:?}", e);
        }
    }

    pub(crate) fn process_tx_queue_event(&mut self) {
        self.process_tx_loop();
    }

    pub(crate) fn process_backend_socket_readable(&mut self) {
        if let Err(e) = self.queues[RX_INDEX].enable_notification(&self.mem) {
            error!("error disabling queue notifications: {:?}", e);
        }
        if let Err(e) = self.process_rx() {
            tracing::error!("Failed to process rx: {e:?} (triggered by backend socket readable)");
        };
        if let Err(e) = self.queues[RX_INDEX].disable_notification(&self.mem) {
            error!("error disabling queue notifications: {:?}", e);
        }
    }

    pub(crate) fn process_backend_socket_writeable(&mut self) {
        match self
            .backend
            .try_finish_write(vnet_hdr_len(), &self.tx_frame_buf[..self.tx_frame_len])
        {
            Ok(()) => self.process_tx_loop(),
            Err(WriteError::PartialWrite | WriteError::NothingWritten) => {}
            Err(e @ WriteError::Internal(_)) => {
                tracing::error!("Failed to finish write: {e:?}");
            }
            Err(e @ WriteError::ProcessNotRunning) => {
                tracing::debug!("Failed to finish write: {e:?}");
            }
        }
    }

    fn process_rx(&mut self) -> result::Result<(), RxError> {
        // if we have a deferred frame we try to process it first,
        // if that is not possible, we don't continue processing other frames
        if self.rx_has_deferred_frame {
            if self.write_frame_to_guest() {
                self.rx_has_deferred_frame = false;
            } else {
                return Ok(());
            }
        }

        let mut signal_queue = false;

        // Read as many frames as possible.
        let result = loop {
            match self.read_into_rx_frame_buf_from_backend() {
                Ok(()) => {
                    if self.write_frame_to_guest() {
                        signal_queue = true;
                    } else {
                        self.rx_has_deferred_frame = true;
                        break Ok(());
                    }
                }
                Err(ReadError::NothingRead) => break Ok(()),
                Err(e @ ReadError::Internal(_)) => break Err(RxError::Backend(e)),
            }
        };

        // At this point we processed as many Rx frames as possible.
        // We have to wake the guest if at least one descriptor chain has been used.
        if signal_queue {
            self.signal_used_queue().map_err(RxError::DeviceError)?;
        }

        result
    }

    fn process_tx_loop(&mut self) {
        loop {
            self.queues[TX_INDEX]
                .disable_notification(&self.mem)
                .unwrap();

            if let Err(e) = self.process_tx() {
                tracing::error!(
                    "Failed to process rx: {e:?} (triggered by backend socket readable)"
                );
            };

            if !self.queues[TX_INDEX]
                .enable_notification(&self.mem)
                .unwrap()
            {
                break;
            }
        }
    }

    fn process_tx(&mut self) -> result::Result<(), TxError> {
        let tx_queue = &mut self.queues[TX_INDEX];

        if self.backend.has_unfinished_write()
            && self
                .backend
                .try_finish_write(vnet_hdr_len(), &self.tx_frame_buf[..self.tx_frame_len])
                .is_err()
        {
            tracing::trace!("Cannot process tx because of unfinished partial write!");
            return Ok(());
        }

        let mut raise_irq = false;

        while let Some(head) = tx_queue.pop(&self.mem) {
            let head_index = head.index;
            let mut read_count = 0;
            let mut next_desc = Some(head);

            self.tx_iovec.clear();
            while let Some(desc) = next_desc {
                if desc.is_write_only() {
                    self.tx_iovec.clear();
                    break;
                }
                self.tx_iovec.push((desc.addr, desc.len as usize));
                read_count += desc.len as usize;
                next_desc = desc.next_descriptor();
            }

            // Copy buffer from across multiple descriptors.
            read_count = 0;
            for (desc_addr, desc_len) in self.tx_iovec.drain(..) {
                let limit = cmp::min(read_count + desc_len, self.tx_frame_buf.len());

                let read_result = self
                    .mem
                    .read_slice(&mut self.tx_frame_buf[read_count..limit], desc_addr);
                match read_result {
                    Ok(()) => {
                        read_count += limit - read_count;
                    }
                    Err(e) => {
                        tracing::error!("Failed to read slice: {:?}", e);
                        read_count = 0;
                        break;
                    }
                }
            }

            self.tx_frame_len = read_count;
            match self
                .backend
                .write_frame(vnet_hdr_len(), &mut self.tx_frame_buf[..read_count])
            {
                Ok(()) => {
                    self.tx_frame_len = 0;
                    tx_queue
                        .add_used(&self.mem, head_index, 0)
                        .map_err(TxError::QueueError)?;
                    raise_irq = true;
                }
                Err(WriteError::NothingWritten) => {
                    tx_queue.undo_pop();
                    break;
                }
                Err(WriteError::PartialWrite) => {
                    tracing::trace!("process_tx: partial write");
                    /*
                    This situation should be pretty rare, assuming reasonably sized socket buffers.
                    We have written only a part of a frame to the backend socket (the socket is full).

                    The frame we have read from the guest remains in tx_frame_buf, and will be sent
                    later.

                    Note that we cannot wait for the backend to process our sending frames, because
                    the backend could be blocked on sending a remainder of a frame to us - us waiting
                    for backend would cause a deadlock.
                     */
                    tx_queue
                        .add_used(&self.mem, head_index, 0)
                        .map_err(TxError::QueueError)?;
                    raise_irq = true;
                    break;
                }
                Err(e @ WriteError::Internal(_) | e @ WriteError::ProcessNotRunning) => {
                    return Err(TxError::Backend(e))
                }
            }
        }

        if raise_irq && tx_queue.needs_notification(&self.mem).unwrap() {
            self.signal_used_queue().map_err(TxError::DeviceError)?;
        }

        Ok(())
    }

    fn signal_used_queue(&mut self) -> result::Result<(), DeviceError> {
        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);
        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
            Ok(())
        } else {
            self.signals.assert(NetSignalMask::INTERRUPT);
            Ok(())
        }
    }

    // Copies a single frame from `self.rx_frame_buf` into the guest.
    fn write_frame_to_guest_impl(&mut self) -> result::Result<(), FrontendError> {
        let mut result: std::result::Result<(), FrontendError> = Ok(());

        let queue = &mut self.queues[RX_INDEX];
        let head_descriptor = queue
            .pop(&self.mem)
            .ok_or_else(|| FrontendError::EmptyQueue)?;
        let head_index = head_descriptor.index;

        let mut frame_slice = &self.rx_frame_buf[..self.rx_frame_buf_len];

        let frame_len = frame_slice.len();
        let mut maybe_next_descriptor = Some(head_descriptor);
        while let Some(descriptor) = &maybe_next_descriptor {
            if frame_slice.is_empty() {
                break;
            }

            if !descriptor.is_write_only() {
                result = Err(FrontendError::ReadOnlyDescriptor);
                break;
            }

            let len = std::cmp::min(frame_slice.len(), descriptor.len as usize);
            match self.mem.write_slice(&frame_slice[..len], descriptor.addr) {
                Ok(()) => {
                    frame_slice = &frame_slice[len..];
                }
                Err(e) => {
                    tracing::error!("Failed to write slice: {:?}", e);
                    result = Err(FrontendError::GuestMemory(e));
                    break;
                }
            };

            maybe_next_descriptor = descriptor.next_descriptor();
        }
        if result.is_ok() && !frame_slice.is_empty() {
            tracing::warn!("Receiving buffer is too small to hold frame of current size");
            result = Err(FrontendError::DescriptorChainTooSmall);
        }

        // Mark the descriptor chain as used. If an error occurred, skip the descriptor chain.
        let used_len = if result.is_err() { 0 } else { frame_len as u32 };
        queue
            .add_used(&self.mem, head_index, used_len)
            .map_err(FrontendError::QueueError)?;
        result
    }

    // Copies a single frame from `self.rx_frame_buf` into the guest. In case of an error retries
    // the operation if possible. Returns true if the operation was successfull.
    fn write_frame_to_guest(&mut self) -> bool {
        let max_iterations = self.queues[RX_INDEX].actual_size();
        for _ in 0..max_iterations {
            match self.write_frame_to_guest_impl() {
                Ok(()) => return true,
                Err(FrontendError::EmptyQueue) => {
                    // retry
                    continue;
                }
                Err(_) => {
                    // retry
                    continue;
                }
            }
        }

        false
    }

    /// Fills self.rx_frame_buf with an ethernet frame from backend and prepends virtio_net_hdr to it
    fn read_into_rx_frame_buf_from_backend(&mut self) -> result::Result<(), ReadError> {
        // we expect backend to return a vnet hdr
        let len = self.backend.read_frame(&mut self.rx_frame_buf)?;
        self.rx_frame_buf_len = len;
        Ok(())
    }
}
