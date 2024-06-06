use std::sync::Arc;

use gruel::{InterestCtrl, RawSignalChannel, Subscriber};
use memmage::CloneDynRef;

use crate::Mutex;

pub struct SubscriberMutexAdapter<T>(pub Arc<Mutex<T>>);

impl<T: Subscriber> Subscriber for SubscriberMutexAdapter<T> {
    fn process_signals(&mut self, ctl: &mut InterestCtrl<'_>) {
        self.0.lock().unwrap().process_signals(ctl)
    }

    fn process_event(&mut self, ctl: &mut InterestCtrl<'_>, event: &mio::event::Event) {
        self.0.lock().unwrap().process_event(ctl, event)
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        self.0.lock().unwrap().signals()
    }

    fn debug_type_name(&self) -> &'static str {
        std::any::type_name::<T>()
    }
}
