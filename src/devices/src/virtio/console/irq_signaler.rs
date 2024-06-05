use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::Arc;
use utils::Mutex;

use crate::legacy::Gic;
use crate::virtio::{VIRTIO_MMIO_INT_CONFIG, VIRTIO_MMIO_INT_VRING};

#[derive(Clone)]
pub struct IRQSignaler {
    interrupt_status: Arc<AtomicUsize>,
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,
}

impl IRQSignaler {
    pub fn new() -> IRQSignaler {
        Self {
            interrupt_status: Arc::new(AtomicUsize::new(0)),
            intc: None,
            irq_line: None,
        }
    }

    pub fn signal_used_queue(&self, reason: &str) {
        tracing::trace!("signal used queue because '{reason}'");
        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_VRING as usize, Ordering::SeqCst);

        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
        }
    }

    #[allow(dead_code)]
    pub fn signal_config_update(&self) {
        self.interrupt_status
            .fetch_or(VIRTIO_MMIO_INT_CONFIG as usize, Ordering::SeqCst);
    }

    pub fn interrupt_status(&self) -> Arc<AtomicUsize> {
        self.interrupt_status.clone()
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.intc = Some(intc);
    }

    pub fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(irq);
    }
}
