// FIXME: We should weaken orderings.

#![allow(dead_code)]

use std::{
    fmt,
    panic::Location,
    ptr::NonNull,
    sync::atomic::{AtomicU64, Ordering},
};

use parking_lot::Mutex;

#[derive(Default)]
pub struct RawSignalChannelInner {
    /// The bit-flag of signals currently asserted on this signal. If a bit is set, it is guaranteed
    /// that either...
    ///
    /// a) the worker thread is eventually going to read it once it reaches
    /// [`RawSignalChannelInner::wait`]'s critical section again.
    ///
    /// b) another thread is waiting to tell the worker thread of this scenario
    ///
    asserted_mask: AtomicU64,

    /// The active waker function. This pointer is guaranteed to be valid to call at the time the
    /// handler is locked.
    #[allow(clippy::type_complexity)]
    waker: Mutex<Option<WakerValue>>,
}

struct WakerValue {
    func: NonNull<dyn FnMut(u64) + Send + Sync>,
    handler_location: &'static Location<'static>,
}

impl RawSignalChannelInner {
    pub fn assert(&self, mask: u64) {
        let prev = self.asserted_mask.fetch_or(mask, Ordering::SeqCst);

        // By invariant, if a bit is set in the mask, the worker thread will eventually observe it.
        // Hence, if all of our set bits are already one, we don't need to worry.
        if prev & mask == mask {
            return;
        }

        // Otherwise, we need to try and wake the worker. If it's a false positive, this will early
        // return in the handler body.
        if let Some(handler) = &mut *self.waker.lock() {
            (unsafe { handler.func.as_mut() })(mask);
        }
    }

    #[track_caller]
    pub fn wait<R>(
        &self,
        wake_mask: u64,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        // Wrap the user-supplied waker with one that can better handle the scenarios thrown at us
        // by this implementation. Specifically, this handler could be called more than once and may
        // be called by an incompatible wake-up mask.
        let mut waker = Some(waker);
        let mut waker = move |mask| {
            if mask & wake_mask == 0u64 {
                return;
            }

            let Some(waker) = waker.take() else {
                return;
            };

            waker();
        };

        // Begin critical section!
        let mut waker_mutex = self.waker.lock();

        assert!(
            waker_mutex.is_none(),
            "`wait` already called at {} on this channel",
            waker_mutex.as_ref().unwrap().handler_location,
        );

        if self.asserted_mask.load(Ordering::SeqCst) & wake_mask != 0 {
            return None;
        }

        *waker_mutex = Some(WakerValue {
            func: unsafe {
                #[allow(clippy::unnecessary_cast)]
                NonNull::new_unchecked(
                    &mut waker as *mut (dyn '_ + FnMut(u64) + Send + Sync)
                        as *mut (dyn FnMut(u64) + Send + Sync),
                )
            },
            handler_location: Location::caller(),
        });

        // End of critical section!
        drop(waker_mutex);

        // Create a guard for before this function ends to ensure that the `waker` closure does not
        // dangle. Since our local is created after our closure local, we will run before it, as
        // expected.
        let _restore_guard = scopeguard::guard((), |()| {
            *self.waker.lock() = None;
        });

        // If we did not take the branch we know that each waker could be in one of three states:
        //
        // 1) It hasn't accessed the `asserted_mask` yet and that's fine. It will see that it can set
        //    a bit and wake us up.
        //
        // 2) It just wrote to the `asserted_mask`, observed that one of its bits was not set, and
        //    is waiting, or is about to be waiting, on the `waker` mutex we're now holding.
        //
        // Notably, however, it cannot be that we are here and that no applicable `wait`ers are about
        // to lock or have locked this `waker` mutex. This is because, either...
        //
        // 1) The original waking thread acquired the handler but was rejected, either because the
        //    handler was none or because the handler wasn't configured to wake on the specified
        //    bits. In this case when we lock the `waker` mutex for ourselves, we must have seen the
        //    set bits and early-returned.
        //
        // 2) All of the `wake` calls were ongoing as the `waker` mutex was locked. In this case, one
        //    of them must have set the bit and observed that fact and is now in the process of waking
        //    us up.
        //
        // Hence, we have satisfied the invariants of the asserted mask.
        Some(worker())
    }

    pub fn take(&self, mask: u64) -> u64 {
        self.asserted_mask.fetch_and(!mask, Ordering::SeqCst) & mask
    }

    pub fn snapshot(&self) -> impl fmt::Debug + Clone {}
}
