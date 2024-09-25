use gruel::{InterestCtrl, RawSignalChannel, Subscriber};
use memmage::{CastableRef, CloneDynRef};

use super::device::{Console, ConsoleSignalMask};
use crate::virtio::device::VirtioDevice;

impl Subscriber for Console {
    type EventMeta = ();

    fn process_signals(&mut self, _ctrl: &mut InterestCtrl<'_, ()>) {
        let taken = self.signals.take(ConsoleSignalMask::all());

        if self.is_activated() {
            let mut raise_irq = false;

            // ignore new CONTROL_RXQ buffers: we only fill it when we have something to send

            if taken.intersects(ConsoleSignalMask::CONTROL_TXQ) {
                raise_irq |= self.process_control_tx();
            }

            if taken.intersects(ConsoleSignalMask::FILL_CONTROL_RXQ) {
                raise_irq |= self.process_control_rx();
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
