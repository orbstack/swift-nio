#![allow(dead_code)]
#![allow(unused_variables)]

use std::fmt;

#[derive(Default)]
pub struct RawSignalChannelInner {}

impl RawSignalChannelInner {
    pub fn assert(&self, mask: u64) {
        unimplemented!();
    }

    pub fn wait<R>(
        &self,
        wake_mask: u64,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        unimplemented!();
    }

    pub fn take(&self, mask: u64) -> u64 {
        unimplemented!();
    }

    pub fn snapshot(&self) -> impl fmt::Debug + Clone {}
}
