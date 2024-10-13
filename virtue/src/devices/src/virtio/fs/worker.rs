use gruel::ParkSignalChannelExt;
use std::sync::Arc;
use std::thread;
use utils::memory::GuestMemory;
use utils::qos::{set_thread_qos, QosClass};

use super::super::{FsError, Queue};
use super::defs::{HPQ_INDEX, REQ_INDEX};
use super::descriptor_utils::{Reader, Writer};
use super::device::{FsSignalChannel, FsSignalMask};
use super::passthrough::PassthroughFs;
use super::server::Server;
use crate::legacy::Gic;

pub struct FsWorker {
    signals: Arc<FsSignalChannel>,
    queues: Vec<Queue>,
    intc: Option<Arc<Gic>>,
    irq_line: Option<u32>,

    mem: GuestMemory,
    server: Arc<Server<PassthroughFs>>,
}

impl FsWorker {
    #[allow(clippy::too_many_arguments)]
    pub fn new(
        signals: Arc<FsSignalChannel>,
        queues: Vec<Queue>,
        intc: Option<Arc<Gic>>,
        irq_line: Option<u32>,
        mem: GuestMemory,
        server: Arc<Server<PassthroughFs>>,
    ) -> Self {
        Self {
            signals,
            queues,
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
                // workers are used for NFS and other async requests, not only for FORGET
                set_thread_qos(QosClass::Utility, None).unwrap();

                self.work()
            })
            .expect("failed to spawn thread")
    }

    fn work(mut self) {
        let handled_mask = FsSignalMask::SHUTDOWN_WORKER | FsSignalMask::HPQ | FsSignalMask::REQ;

        loop {
            self.signals.wait_on_park(handled_mask);
            let taken = self.signals.take(handled_mask);

            if taken.intersects(FsSignalMask::SHUTDOWN_WORKER) {
                return;
            }

            if taken.intersects(FsSignalMask::HPQ) {
                self.handle_event(HPQ_INDEX);
            }

            if taken.intersects(FsSignalMask::REQ) {
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
        let queue = &mut self.queues[queue_index];
        while let Some(head) = queue.pop(&self.mem) {
            let reader = Reader::new(&self.mem, head.clone())
                .map_err(FsError::QueueReader)
                .unwrap();
            let writer = Writer::new(&self.mem, head.clone())
                .map_err(FsError::QueueWriter)
                .unwrap();

            if let Err(e) = self.server.handle_message(reader, writer) {
                error!("error handling message: {:?}", e);
            }

            if let Err(e) = queue.add_used(&self.mem, head.index, 0) {
                error!("failed to add used elements to the queue: {:?}", e);
            }

            if queue.needs_notification(&self.mem).unwrap() {
                if let Some(intc) = &self.intc {
                    intc.set_irq(self.irq_line.unwrap());
                }
            }
        }
    }
}
