use gruel::{InterestCtrl, RawSignalChannel, Subscriber};
use memmage::{CastableRef, CloneDynRef};

use super::device::{get_win_size, Console, ConsoleSignalMask};
use crate::virtio::console::port_queue_mapping::{queue_idx_to_port_id, QueueDirection};
use crate::virtio::device::VirtioDevice;

impl Console {
    fn notify_port_queue_event(&mut self, queue_index: usize) {
        let (direction, port_id) = queue_idx_to_port_id(queue_index);
        match direction {
            QueueDirection::Rx => {
                tracing::trace!("Notify rx (queue event)");
                self.ports[port_id].notify_rx()
            }
            QueueDirection::Tx => {
                tracing::trace!("Notify tx (queue event)");
                self.ports[port_id].notify_tx()
            }
        }
    }

    fn handle_sigwinch_event(&mut self) {
        debug!("console: SIGWINCH event");

        let (cols, rows) = get_win_size();
        self.update_console_size(cols, rows);
    }
}

impl Subscriber for Console {
    type EventMeta = ();

    fn process_signals(&mut self, _ctrl: &mut InterestCtrl<'_, ()>) {
        let taken = self.signals.take(ConsoleSignalMask::all());

        if self.is_activated() {
            let mut raise_irq = false;

            if taken.intersects(ConsoleSignalMask::CONTROL_TXQ) {
                raise_irq |= self.process_control_tx();
            }

            if taken.intersects(ConsoleSignalMask::CONTROL_RXQ_CONTROL) {
                raise_irq |= self.process_control_rx();
            }

            // TODO: add back multi-port support
            if taken.intersects(ConsoleSignalMask::RXQ) {
                raise_irq = true;
                self.notify_port_queue_event(0);
            }

            if taken.intersects(ConsoleSignalMask::TXQ) {
                raise_irq = true;
                self.notify_port_queue_event(1);
            }

            if taken.intersects(ConsoleSignalMask::SIGWINCH) {
                self.handle_sigwinch_event();
            }

            if raise_irq {
                self.irq.signal_used_queue("event_handler");
            }
        }
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        vec![CloneDynRef::new(
            self.signals.clone().map(|v| v.raw() as &RawSignalChannel),
        )]
    }
}
