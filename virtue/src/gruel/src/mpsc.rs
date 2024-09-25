use std::sync::Arc;

use crossbeam_queue::ArrayQueue;

use crate::{AnySignalChannel, ArcBoundSignalChannel, BoundSignalChannel};

#[derive(thiserror::Error, Debug)]
pub enum MpscError {
    #[error("queue is full")]
    Full,
}

pub struct SignalMpsc<T> {
    queue: ArrayQueue<T>,
    signal: ArcBoundSignalChannel,
}

impl<T> SignalMpsc<T> {
    pub fn with_capacity<C: AnySignalChannel>(
        capacity: usize,
        signal: &Arc<C>,
        mask: C::Mask,
    ) -> Self {
        Self {
            queue: ArrayQueue::new(capacity),
            signal: BoundSignalChannel::new(signal.clone(), mask),
        }
    }

    pub fn send_or_return(&self, item: T) -> Result<(), T> {
        let result = self.queue.push(item);
        if result.is_ok() {
            self.signal.assert();
            Ok(())
        } else {
            result
        }
    }

    pub fn send(&self, item: T) -> Result<(), MpscError> {
        if self.queue.push(item).is_ok() {
            self.signal.assert();
            Ok(())
        } else {
            Err(MpscError::Full)
        }
    }

    pub fn force_send(&self, item: T) -> Option<T> {
        let old_item = self.queue.force_push(item);
        self.signal.assert();
        old_item
    }

    pub fn recv_one(&self) -> Option<T> {
        self.queue.pop()
    }

    pub fn recv_all(&self, mut f: impl FnMut(T)) {
        while let Some(item) = self.queue.pop() {
            f(item);
        }
    }
}
