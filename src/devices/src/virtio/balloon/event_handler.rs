use std::sync::Arc;

use gruel::{ArcSignalChannelExt, RawSignalChannel, SignalMultiplexHandler};
use memmage::CloneDynRef;

use super::device::Balloon;
use crate::virtio::balloon::device::BalloonDeviceSignalMask;

impl Balloon {
    pub(crate) fn handle_ifq_event(&mut self) {
        error!("balloon: unsupported inflate queue event");
    }

    pub(crate) fn handle_dfq_event(&mut self) {
        error!("balloon: unsupported deflate queue event");
    }

    pub(crate) fn handle_stq_event(&mut self) {
        debug!("balloon: stats queue event (ignored)");
    }

    pub(crate) fn handle_phq_event(&mut self) {
        error!("balloon: unsupported page-hinting queue event");
    }

    pub(crate) fn handle_frq_event(&mut self) {
        debug!("balloon: free-page reporting queue event");

        if self.process_frq() {
            self.signal_used_queue();
        }
    }
}

impl SignalMultiplexHandler for Balloon {
    fn process(&mut self) {
        let taken = self.signal.take(BalloonDeviceSignalMask::all());

        if taken.intersects(BalloonDeviceSignalMask::IFQ) {
            self.handle_ifq_event();
        }

        if taken.intersects(BalloonDeviceSignalMask::DFQ) {
            self.handle_dfq_event();
        }

        if taken.intersects(BalloonDeviceSignalMask::STQ) {
            self.handle_stq_event();
        }

        if taken.intersects(BalloonDeviceSignalMask::PHQ) {
            self.handle_phq_event();
        }

        if taken.intersects(BalloonDeviceSignalMask::FRQ) {
            self.handle_frq_event();
        }
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        vec![CloneDynRef::new(
            self.signal.clone().into_raw() as Arc<RawSignalChannel>
        )]
    }
}
