use crate::legacy::Gic;
use crate::virtio::descriptor_utils::Iovec;
use crate::virtio::net::{QUEUE_SIZE, RX_INDEX, TX_INDEX};
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
use std::{mem, result};
use utils::Mutex;
use virtio_bindings::virtio_net::virtio_net_hdr_v1;
use vm_memory::{GuestMemory, GuestMemoryMmap};

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

    iovecs: Vec<Iovec<'static>>,
    mtu: u16,
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
        mtu: u16,
    ) -> Self {
        let backend = match cfg_backend {
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

            iovecs: Vec::with_capacity(QUEUE_SIZE as usize),
            mtu,
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

            if taken.intersects(NET_QUEUE_SIGS.get(TX_INDEX)) {
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
                        self.process_backend_socket_readable();
                    }

                    if event.is_writable() {
                        self.process_backend_socket_writeable();
                    }
                }
            }
        }
    }

    pub(crate) fn process_rx_queue_event(&mut self) {
        if let Err(e) = self.queues[RX_INDEX].disable_notification(&self.mem) {
            error!("error disabling queue notifications: {:?}", e);
        }

        if let Err(e) = self.process_rx_loop() {
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
        if let Err(e) = self.process_rx_loop() {
            tracing::error!("Failed to process rx: {e:?} (triggered by backend socket readable)");
        };
        if let Err(e) = self.queues[RX_INDEX].disable_notification(&self.mem) {
            error!("error disabling queue notifications: {:?}", e);
        }
    }

    pub(crate) fn process_backend_socket_writeable(&mut self) {
        self.process_tx_loop()
    }

    fn process_rx_loop(&mut self) -> result::Result<(), RxError> {
        let mut signal_queue = false;

        // Read as many frames as possible.
        let result = loop {
            match self.read_frame_to_guest() {
                Ok(()) => {
                    signal_queue = true;
                }
                // backend socket readable will trigger this
                Err(FrontendError::Backend(ReadError::NothingRead)) => break Ok(()),
                // rx queue signal will trigger this
                Err(FrontendError::EmptyQueue) => break Ok(()),
                Err(e) => break Err(RxError::Frontend(e)),
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

        let mut raise_irq = false;

        while let Some(head) = tx_queue.pop(&self.mem) {
            let head_index = head.index;
            let mut next_desc = Some(head);

            self.iovecs.clear();
            while let Some(desc) = next_desc {
                if desc.is_write_only() {
                    self.iovecs.clear();
                    break;
                }

                match self.mem.get_slice(desc.addr, desc.len as usize) {
                    Ok(vs) => {
                        self.iovecs.push(Iovec::from_static(vs));
                    }
                    Err(e) => {
                        tracing::error!("Failed to get slice: {:?}", e);
                        break;
                    }
                }

                next_desc = desc.next_descriptor();
            }

            match self.backend.write_frame(vnet_hdr_len(), &mut self.iovecs) {
                Ok(()) => {
                    tx_queue
                        .add_used(&self.mem, head_index, 0)
                        .map_err(TxError::QueueError)?;
                    raise_irq = true;
                }
                Err(WriteError::NothingWritten) => {
                    // we'll get a writable event when the backend is ready again
                    // stop processing the queue, as nothing will go through
                    // with tiny buffers (64k buf = 1 pkt), this is *much* better for throughput: 75 gbps vs. 50 mbos
                    tx_queue.undo_pop();
                    break;
                }
                Err(e @ WriteError::Internal(_) | e @ WriteError::ProcessNotRunning) => {
                    // notify and drop the packet
                    tx_queue
                        .add_used(&self.mem, head_index, 0)
                        .map_err(TxError::QueueError)?;
                    raise_irq = true;

                    error!("Failed to write frame to backend: {:?}", e);
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
    fn read_frame_to_guest(&mut self) -> result::Result<(), FrontendError> {
        let queue = &mut self.queues[RX_INDEX];
        let head_descriptor = queue
            .pop(&self.mem)
            .ok_or_else(|| FrontendError::EmptyQueue)?;
        let head_index = head_descriptor.index;

        let result = (|| {
            self.iovecs.clear();
            let mut total_len = 0;
            let mut maybe_next_descriptor = Some(head_descriptor);
            while let Some(descriptor) = &maybe_next_descriptor {
                if !descriptor.is_write_only() {
                    return Err(FrontendError::ReadOnlyDescriptor);
                }

                let vs = self
                    .mem
                    .get_slice(descriptor.addr, descriptor.len as usize)
                    .map_err(FrontendError::GuestMemory)?;
                self.iovecs.push(Iovec::from_static(vs));
                total_len += vs.len();

                maybe_next_descriptor = descriptor.next_descriptor();
            }
            if total_len < vnet_hdr_len() + self.mtu as usize {
                tracing::error!("Receiving buffer is too small to hold frame of max size");
                return Err(FrontendError::DescriptorChainTooSmall);
            }

            let frame_len = self
                .backend
                .read_frame(&self.iovecs)
                .map_err(FrontendError::Backend)?;
            Ok(frame_len)
        })();

        match result {
            Ok(used_len) => {
                // Mark the descriptor chain as used.
                queue
                    .add_used(&self.mem, head_index, used_len as u32)
                    .map_err(FrontendError::QueueError)?;
                Ok(())
            }
            Err(FrontendError::Backend(ReadError::NothingRead)) => {
                // If the backend has no data to read, push the head descriptor back to the queue.
                queue.undo_pop();
                Err(FrontendError::Backend(ReadError::NothingRead))
            }
            Err(e) => {
                // Mark the descriptor chain as used. If an error occurred, skip the descriptor chain.
                queue
                    .add_used(&self.mem, head_index, 0)
                    .map_err(FrontendError::QueueError)?;
                Err(e)
            }
        }
    }
}
