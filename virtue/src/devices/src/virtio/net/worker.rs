use crate::legacy::Gic;
use crate::virtio::descriptor_utils::Iovec;
use crate::virtio::net::{QUEUE_SIZE, RX_INDEX, TX_INDEX};
use crate::virtio::Queue;
use crate::Error as DeviceError;

use super::backend::{NetBackend, ReadError, WriteError};
use super::device::{
    FrontendError, NetSignalChannel, NetSignalMask, RxError, TxError, VirtioNetBackend,
};

use std::ops::{Deref, DerefMut};
use std::sync::Arc;
use std::thread::{self, JoinHandle};
use std::{mem, result};
use utils::memory::GuestMemory;
use virtio_bindings::virtio_net::virtio_net_hdr_v1;

use gruel::{MioChannelExt, OnceMioWaker, ParkSignalChannelExt};

fn vnet_hdr_len() -> usize {
    mem::size_of::<virtio_net_hdr_v1>()
}

pub struct NetWorker {
    signals: Arc<NetSignalChannel>,
    queues: Vec<Queue>,
    intc: Option<Arc<Gic>>,
    irq_line: Option<u32>,

    mem: GuestMemory,
    backend: Box<dyn NetBackend + Send>,

    iovecs_buf: IovecsBuffer,
    mtu: u16,
}

impl NetWorker {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        signals: Arc<NetSignalChannel>,
        queues: Vec<Queue>,
        intc: Option<Arc<Gic>>,
        irq_line: Option<u32>,
        mem: GuestMemory,
        cfg_backend: VirtioNetBackend,
        mtu: u16,
    ) -> Self {
        let backend = cfg_backend.create(&queues, &mem, &intc, &irq_line);

        Self {
            signals,
            queues,
            intc,
            irq_line,

            mem,
            backend,

            iovecs_buf: IovecsBuffer::with_capacity(QUEUE_SIZE as usize),
            mtu,
        }
    }

    pub fn run(self) -> JoinHandle<()> {
        thread::Builder::new()
            .name("net worker".to_string())
            .spawn(|| match self.backend.raw_socket_fd() {
                Some(backend_socket_fd) => self.work_mio(backend_socket_fd),
                None => self.work_park(),
            })
            .expect("failed to spawn thread")
    }

    fn work_mio(mut self, backend_socket_fd: i32) {
        // Setup epoll
        let mut poll = mio::Poll::new().unwrap();
        let mut events = mio::Events::with_capacity(32);

        let backend_socket_token = mio::Token(0);
        poll.registry()
            .register(
                &mut mio::unix::SourceFd(&backend_socket_fd),
                backend_socket_token,
                mio::Interest::READABLE | mio::Interest::WRITABLE,
            )
            .unwrap();

        let waker_token = mio::Token(1);
        let waker = mio::Waker::new(poll.registry(), waker_token).unwrap();
        self.signals
            .waker_state::<OnceMioWaker>()
            .set_waker(Arc::new(waker));

        let handled_mask =
            NetSignalMask::SHUTDOWN_WORKER | NetSignalMask::GUEST_RXQ | NetSignalMask::GUEST_TXQ;

        // Start worker loop
        loop {
            if let Err(e) = self
                .signals
                .wait_on_poll(handled_mask, &mut poll, &mut events, None)
            {
                error!("Failed to wait on poll: {:?}", e);
                break;
            }

            // Handle signals
            let taken = self.signals.take(handled_mask);

            if taken.intersects(NetSignalMask::SHUTDOWN_WORKER) {
                return;
            }

            if taken.intersects(NetSignalMask::GUEST_RXQ) {
                self.process_rx_queue_event();
            }

            if taken.intersects(NetSignalMask::GUEST_TXQ) {
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

    fn work_park(mut self) {
        // we don't need to handle guest RXQ: writing to guest is only done by the backend callback
        // gruel doesn't wake us up if an asserted signal isn't in this mask, so this saves wakeups
        let handled_mask = NetSignalMask::SHUTDOWN_WORKER | NetSignalMask::GUEST_TXQ;

        loop {
            self.signals.wait_on_park(handled_mask);

            let taken = self.signals.take(handled_mask);

            if taken.intersects(NetSignalMask::SHUTDOWN_WORKER) {
                return;
            }

            if taken.intersects(NetSignalMask::GUEST_TXQ) {
                self.process_tx_queue_event();
            }
        }
    }

    pub(crate) fn process_rx_queue_event(&mut self) -> bool {
        self.queues[RX_INDEX]
            .disable_notification(&self.mem)
            .unwrap();

        if let Err(e) = self.process_rx_loop() {
            tracing::error!("Failed to process rx: {e:?} (triggered by queue event)")
        };

        self.queues[RX_INDEX]
            .enable_notification(&self.mem)
            .unwrap()
    }

    pub(crate) fn process_tx_queue_event(&mut self) {
        self.process_tx_loop();
    }

    pub(crate) fn process_backend_socket_readable(&mut self) -> bool {
        self.queues[RX_INDEX]
            .disable_notification(&self.mem)
            .unwrap();

        if let Err(e) = self.process_rx_loop() {
            tracing::error!("Failed to process rx: {e:?} (triggered by backend socket readable)");
        };

        self.queues[RX_INDEX]
            .enable_notification(&self.mem)
            .unwrap()
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

            let mut iovecs = self.iovecs_buf.clear();
            while let Some(desc) = next_desc {
                if desc.is_write_only() {
                    break;
                }

                match self.mem.get_slice(desc.addr, desc.len as usize) {
                    Ok(vs) => {
                        iovecs.push(Iovec::from(vs));
                    }
                    Err(e) => {
                        tracing::error!("Failed to get slice: {:?}", e);
                        break;
                    }
                }

                next_desc = desc.next_descriptor();
            }

            match self.backend.write_frame(vnet_hdr_len(), &mut iovecs) {
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
        if let Some(intc) = &self.intc {
            intc.set_irq(self.irq_line.unwrap());
        }
        Ok(())
    }

    // Copies a single frame from `self.rx_frame_buf` into the guest.
    fn read_frame_to_guest(&mut self) -> result::Result<(), FrontendError> {
        let queue = &mut self.queues[RX_INDEX];
        let head_descriptor = queue
            .pop(&self.mem)
            .ok_or(FrontendError::EmptyQueue)?;
        let head_index = head_descriptor.index;

        let result = (|| {
            let mut iovecs = self.iovecs_buf.clear();
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
                iovecs.push(Iovec::from(vs));
                total_len += vs.len();

                maybe_next_descriptor = descriptor.next_descriptor();
            }
            if total_len < vnet_hdr_len() + self.mtu as usize {
                tracing::error!("Receiving buffer is too small to hold frame of max size");
                return Err(FrontendError::DescriptorChainTooSmall);
            }

            let frame_len = self
                .backend
                .read_frame(&iovecs)
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

// allow reusing a buffer for iovecs, but bind lifetime to the usage scope
pub struct IovecsBuffer(Vec<Iovec<'static>>);

impl IovecsBuffer {
    pub fn with_capacity(capacity: usize) -> Self {
        Self(Vec::with_capacity(capacity))
    }

    pub fn clear<'a>(&'a mut self) -> IovecsBufferRef<'a> {
        let r = unsafe {
            std::mem::transmute::<&mut Vec<Iovec<'static>>, &mut Vec<Iovec<'a>>>(&mut self.0)
        };
        IovecsBufferRef(r)
    }
}

pub struct IovecsBufferRef<'a>(&'a mut Vec<Iovec<'a>>);

impl<'a> Deref for IovecsBufferRef<'a> {
    type Target = Vec<Iovec<'a>>;

    fn deref(&self) -> &Self::Target {
        self.0
    }
}

impl DerefMut for IovecsBufferRef<'_> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        self.0
    }
}

impl Drop for IovecsBufferRef<'_> {
    fn drop(&mut self) {
        self.0.clear();
    }
}
