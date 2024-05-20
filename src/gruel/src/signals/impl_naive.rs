#![allow(dead_code)]

use std::{fmt, panic::Location, ptr::NonNull};

use parking_lot::Mutex;

#[derive(Default)]
pub struct RawSignalChannelInner {
    state: Mutex<RawSignalChannelInnerLocked>,
}

#[derive(Default)]
struct RawSignalChannelInnerLocked {
    // The set of all asserted bits.
    asserted_mask: u64,

    // The set of all bits listened for by the current waker.
    wake_mask: u64,

    // Only valid if `wake_mask` is not zero.
    handler: Option<NonNull<dyn FnMut() + Send + Sync>>,

    // For debugging purposes
    handler_location: Option<&'static Location<'static>>,
}

#[derive(Debug, Copy, Clone)]
struct RawSignalChannelSnapshot {
    asserted_mask: u64,
    wake_mask: u64,
    handler: Option<&'static Location<'static>>,
}

impl RawSignalChannelInner {
    pub fn assert(&self, mask: u64) {
        let mut state = self.state.lock();

        state.asserted_mask |= mask;
        if state.wake_mask & mask != 0 {
            // (implies that state.wake_mask != 0)
            unsafe { (*state.handler.unwrap().as_ptr())() }
        }
    }

    #[track_caller]
    pub fn wait<R>(
        &self,
        wake_mask: u64,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        // Unsize the waker
        let mut waker = Some(waker);
        let mut waker = || {
            let Some(waker) = waker.take() else {
                return;
            };
            (waker)();
        };
        let waker = &mut waker as &mut (dyn '_ + FnMut() + Send + Sync);

        // Bind the waker
        let mut state = self.state.lock();

        assert!(
            state.handler.is_none(),
            "`wait` already called at {} on this channel",
            state.handler_location.unwrap(),
        );

        if state.asserted_mask & wake_mask != 0 {
            return None;
        }

        #[allow(clippy::unnecessary_cast)] // false positive
        {
            state.handler_location = Some(Location::caller());
            state.handler = NonNull::new(
                waker as *mut (dyn '_ + FnMut() + Send + Sync) as *mut (dyn FnMut() + Send + Sync),
            );
        }

        state.wake_mask = wake_mask;
        drop(state);

        // Run the task with a guard to clear the waker before we invalidate it by leaving this
        // function.
        let _guard = scopeguard::guard((), |()| {
            let mut state = self.state.lock();
            // Mark the handler as invalid so people don't try to wake us up with a dead handler.
            state.wake_mask = 0;
            state.handler = None;
            state.handler_location = None;
        });

        Some(worker())
    }

    pub fn take(&self, mask: u64) -> u64 {
        let mut state = self.state.lock();

        let taken = state.asserted_mask & mask;
        state.asserted_mask &= !mask;
        taken
    }

    pub fn snapshot(&self) -> impl fmt::Debug + Clone {
        let state = self.state.lock();

        RawSignalChannelSnapshot {
            asserted_mask: state.asserted_mask,
            wake_mask: state.wake_mask,
            handler: state.handler_location,
        }
    }
}
