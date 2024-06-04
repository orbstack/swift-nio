use std::sync::Arc;

use gruel::{MioDispatcher, RawSignalChannel, SignalChannel, SignalMultiplexHandler};
use memmage::{CastableRef, CloneDynRef};

use super::device::Balloon;
use crate::virtio::balloon::device::BalloonSignalMask;

impl SignalMultiplexHandler<MioDispatcher> for Balloon {
    fn process(&mut self, _dispatcher: &mut MioDispatcher) {
        let taken = self.signal.take(BalloonSignalMask::all());

        if taken.intersects(BalloonSignalMask::IFQ) {
            error!("balloon: unsupported inflate queue event");
        }

        if taken.intersects(BalloonSignalMask::DFQ) {
            error!("balloon: unsupported deflate queue event");
        }

        if taken.intersects(BalloonSignalMask::STQ) {
            debug!("balloon: stats queue event (ignored)");
        }

        if taken.intersects(BalloonSignalMask::PHQ) {
            error!("balloon: unsupported page-hinting queue event");
        }

        if taken.intersects(BalloonSignalMask::FRQ) {
            debug!("balloon: free-page reporting queue event");

            if self.process_frq() {
                self.signal_used_queue();
            }
        }
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        vec![CloneDynRef::new(
            self.signal.clone().map(SignalChannel::raw) as Arc<RawSignalChannel>,
        )]
    }
}
