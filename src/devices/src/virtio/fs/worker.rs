use gruel::ParkSignalChannelExt;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use std::thread;
use utils::qos::{set_thread_qos, QosClass};
use utils::Mutex;

use vm_memory::GuestMemoryMmap;

use super::super::{FsError, Queue, VIRTIO_MMIO_INT_VRING};
use super::defs::{HPQ_INDEX, REQ_INDEX};
use super::descriptor_utils::{Reader, Writer};
use super::device::{FsSignalChannel, FsSignalMask, FS_QUEUE_SIGS};
use super::passthrough::PassthroughFs;
use super::server::{HostContext, Server};
use crate::legacy::Gic;

pub struct FsWorker {
    signals: Arc<FsSignalChannel>,
    queues: Vec<Queue>,
    interrupt_status: Arc<AtomicUsize>,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,

    mem: GuestMemoryMmap,
    server: Arc<Server<PassthroughFs>>,
}

impl FsWorker {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        signals: Arc<FsSignalChannel>,
        queues: Vec<Queue>,
        interrupt_status: Arc<AtomicUsize>,
        intc: Option<Arc<Mutex<Gic>>>,
        irq_line: Option<u32>,
        mem: GuestMemoryMmap,
        server: Arc<Server<PassthroughFs>>,
    ) -> Self {
        Self {
            signals,
            queues,
            interrupt_status,
            intc,
            irq_line,

            mem,
            server,
        }
    }

    pub fn run(self) -> thread::JoinHandle<()> {
        thread::Builder::new()
            .name(format!("fs{} worker", self.server.hvc_id().unwrap_or(0)))
            .spawn(|| {
                // these worker threads are only used for FORGET
                set_thread_qos(QosClass::Background, None).unwrap();

                self.work()
            })
            .expect("failed to spawn thread")
    }

    fn work(mut self) {
        let virtq_hpq_ev_fd = FS_QUEUE_SIGS.get(HPQ_INDEX);
        let virtq_req_ev_fd = FS_QUEUE_SIGS.get(REQ_INDEX);
        let stop_ev_fd = FsSignalMask::SHUTDOWN_WORKER;
        let handled = virtq_hpq_ev_fd | virtq_req_ev_fd | stop_ev_fd;

        loop {
            self.signals.wait_on_park(handled);
            let taken = self.signals.take(handled);

            if taken.intersects(stop_ev_fd) {
                return;
            }

            if taken.intersects(virtq_hpq_ev_fd) {
                self.handle_event(HPQ_INDEX);
            }

            if taken.intersects(virtq_req_ev_fd) {
                self.handle_event(REQ_INDEX);
            }
        }
    }

    fn handle_event(&mut self, queue_index: usize) {
        debug!("Fs: queue event: {}", queue_index);

        loop {
            self.queues[queue_index]
                .disable_notification(&self.mem)
                .unwrap();

            self.process_queue(queue_index);

            if !self.queues[queue_index]
                .enable_notification(&self.mem)
                .unwrap()
            {
                break;
            }
        }
    }

    fn process_queue(&mut self, queue_index: usize) {
        let hctx = HostContext {
            is_sync_call: false,
        };

        let queue = &mut self.queues[queue_index];
        while let Some(head) = queue.pop(&self.mem) {
            let reader = Reader::new(&self.mem, head.clone())
                .map_err(FsError::QueueReader)
                .unwrap();
            let writer = Writer::new(&self.mem, head.clone())
                .map_err(FsError::QueueWriter)
                .unwrap();

            if let Err(e) = self.server.handle_message(hctx, reader, writer, None) {
                error!("error handling message: {:?}", e);
            }

            if let Err(e) = queue.add_used(&self.mem, head.index, 0) {
                error!("failed to add used elements to the queue: {:?}", e);
            }

            if queue.needs_notification(&self.mem).unwrap() {
                self.interrupt_status
                    .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);
                if let Some(intc) = &self.intc {
                    intc.lock().unwrap().set_irq(self.irq_line.unwrap());
                }
            }
        }
    }
}
