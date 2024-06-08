use gruel::{InterestCtrl, RawSignalChannel, Subscriber};
use memmage::{CastableRef, CloneDynRef};

use super::device::{get_win_size, Console, ConsoleSignalMask, CONSOLE_QUEUE_SIGS};
use crate::virtio::console::device::{CONTROL_RXQ_INDEX, CONTROL_TXQ_INDEX};
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

            if taken.intersects(CONSOLE_QUEUE_SIGS.get(CONTROL_TXQ_INDEX)) {
                raise_irq |= self.process_control_tx();
            }

            if taken.intersects(ConsoleSignalMask::CONTROL_RXQ_CONTROL) {
                raise_irq |= self.process_control_rx();
            }

            if taken.intersects(CONSOLE_QUEUE_SIGS.get(CONTROL_RXQ_INDEX)) {
                raise_irq = true;
            }

            for queue_index in 0..self.queues.len() {
                if queue_index == CONTROL_TXQ_INDEX || queue_index == CONTROL_RXQ_INDEX {
                    continue;
                }

                if taken.intersects(CONSOLE_QUEUE_SIGS.get(queue_index)) {
                    raise_irq = true;
                    self.notify_port_queue_event(queue_index);
                }
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
