use std::sync::Arc;
use utils::Mutex;

use crate::legacy::Gic;

#[derive(Clone)]
pub struct IRQSignaler {
    intc: Option<Arc<Mutex<Gic>>>,
    irq_line: Option<u32>,
}

impl IRQSignaler {
    pub fn new() -> IRQSignaler {
        Self {
            intc: None,
            irq_line: None,
        }
    }

    pub fn signal_used_queue(&self, reason: &str) {
        tracing::trace!("signal used queue because '{reason}'");

        if let Some(intc) = &self.intc {
            intc.lock().unwrap().set_irq(self.irq_line.unwrap());
        }
    }

    #[allow(dead_code)]
    pub fn signal_config_update(&self) {
        todo!();
    }

    pub fn set_intc(&mut self, intc: Arc<Mutex<Gic>>) {
        self.intc = Some(intc);
    }

    pub fn set_irq_line(&mut self, irq: u32) {
        self.irq_line = Some(irq);
    }
}
