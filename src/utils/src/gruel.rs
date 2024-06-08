use std::sync::Arc;

use gruel::{InterestCtrl, RawSignalChannel, Subscriber};
use memmage::CloneDynRef;

use crate::Mutex;

pub struct SubscriberMutexAdapter<T>(pub Arc<Mutex<T>>);

impl<T: Subscriber> Subscriber for SubscriberMutexAdapter<T> {
    type EventMeta = T::EventMeta;

    fn process_signals(&mut self, ctl: &mut InterestCtrl<'_, Self::EventMeta>) {
        self.0.lock().unwrap().process_signals(ctl)
    }

    fn process_event(
        &mut self,
        ctl: &mut InterestCtrl<'_, Self::EventMeta>,
        event: &mio::event::Event,
        meta: &mut Self::EventMeta,
    ) {
        self.0.lock().unwrap().process_event(ctl, event, meta)
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.0.lock().unwrap().signals()
    }

    fn init_interests(&self, ctrl: &mut InterestCtrl<'_, Self::EventMeta>) {
        self.0.lock().unwrap().init_interests(ctrl)
    }

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<T>()
    }
}
