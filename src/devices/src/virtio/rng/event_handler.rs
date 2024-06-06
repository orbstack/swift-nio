use gruel::{InterestCtrl, RawSignalChannel, Subscriber};
use memmage::{CastableRef, CloneDynRef};

use super::device::{Rng, RngSignalMask};
use crate::virtio::device::VirtioDevice;

impl Rng {
    pub(crate) fn handle_req_event(&mut self) {
        if self.process_req() {
            self.signal_used_queue().unwrap();
        }
    }
}

impl Subscriber for Rng {
    fn process_signals(&mut self, _ctrl: &mut InterestCtrl<'_>) {
        let taken = self.signals.take(RngSignalMask::all());

        if self.is_activated() && taken.intersects(RngSignalMask::REQ_QUEUE) {
            self.handle_req_event();
        }
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        vec![CloneDynRef::new(
            self.signals.clone().map(|v| v.raw() as &RawSignalChannel),
        )]
    }
}
