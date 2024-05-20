use std::{fmt, hash, marker::PhantomData, sync::Arc};

use bitflags::Flags;

use crate::util::Parker;

// === "Backends" === //

mod impl_dummy;
mod impl_naive;
mod impl_optimized;

// FIXME: Looks like the optimized backend is broken again!
use impl_naive::RawSignalChannelInner;

// === RawSignalChannel === //

#[derive(Clone, Default)]
pub struct RawSignalChannel {
    inner: Arc<RawSignalChannelInner>,
}

impl fmt::Debug for RawSignalChannel {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.snapshot().fmt(f)
    }
}

unsafe impl Send for RawSignalChannel {}
unsafe impl Sync for RawSignalChannel {}

impl hash::Hash for RawSignalChannel {
    fn hash<H: hash::Hasher>(&self, state: &mut H) {
        Arc::as_ptr(&self.inner).hash(state);
    }
}

impl Eq for RawSignalChannel {}

impl PartialEq for RawSignalChannel {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.inner, &other.inner)
    }
}

impl RawSignalChannel {
    pub fn new() -> Self {
        Self::default()
    }

    /// Asserts zero or more signals.
    pub fn assert(&self, mask: u64) {
        self.inner.assert(mask);
    }

    /// Runs the `worker` routine so long as signals in the `wake_mask` are not asserted. If one of
    /// these signals is asserted during the period of this method's execution, we'll either exit
    /// immediately with `None` or call the waker.
    ///
    /// ## Semantics
    ///
    /// - Spurious wake-up calls are not possible.
    /// - The `waker` may be called at any time, even before `worker` has executed.
    /// - The `waker` may be called more than once.
    /// - `worker` may possibly never execute if the task is cancelled immediately.
    /// - The call to `open` cannot complete until the `waker` terminates.
    ///
    #[track_caller]
    pub fn wait<R>(
        &self,
        wake_mask: u64,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        self.inner.wait(wake_mask, waker, worker)
    }

    /// Takes all signals under the specified mask, clearing them in the process.
    pub fn take(&self, mask: u64) -> u64 {
        self.inner.take(mask)
    }

    /// Fetches a snapshot of the channel's state for debugging purposes.
    pub fn snapshot(&self) -> impl fmt::Debug + Clone {
        self.inner.snapshot()
    }
}

// === SignalChannel === //

pub struct SignalChannel<S> {
    _ty: PhantomData<fn() -> S>,
    raw: RawSignalChannel,
}

impl<S: fmt::Debug + Flags<Bits = u64>> fmt::Debug for SignalChannel<S> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.snapshot().fmt(f)
    }
}

impl<S> Default for SignalChannel<S> {
    fn default() -> Self {
        Self {
            _ty: PhantomData,
            raw: RawSignalChannel::new(),
        }
    }
}

impl<S> Clone for SignalChannel<S> {
    fn clone(&self) -> Self {
        Self {
            _ty: PhantomData,
            raw: self.raw.clone(),
        }
    }
}

impl<S> SignalChannel<S> {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn from_raw(raw: RawSignalChannel) -> Self {
        Self {
            _ty: PhantomData,
            raw,
        }
    }

    pub fn raw(&self) -> &RawSignalChannel {
        &self.raw
    }

    pub fn into_raw(self) -> RawSignalChannel {
        self.raw
    }
}

impl<S: Flags<Bits = u64>> SignalChannel<S> {
    /// Asserts zero or more signals.
    pub fn assert(&self, mask: S) {
        self.raw.assert(mask.bits());
    }

    /// Runs the `runner` routine so long as signals in the `wake_mask` are not asserted. If one of
    /// these signals is asserted during the period of this method's execution, we'll either exit
    /// immediately with `None` or call the waker.
    ///
    /// ## Semantics
    ///
    /// - Spurious wake-up calls are not possible.
    /// - The `waker` may be called at any time, even before `runner` has executed.
    /// - The `waker` may be called more than once.
    /// - `runner` may possibly never execute if the task is cancelled immediately.
    /// - The call to `open` cannot complete until the `waker` terminates.
    ///
    #[track_caller]
    pub fn wait<R>(
        &self,
        wake_mask: S,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        self.raw.wait(wake_mask.bits(), waker, worker)
    }

    /// Honors all signals under the specified mask, clearing them in the process.
    pub fn take(&self, mask: S) -> S {
        S::from_bits_retain(self.raw.take(mask.bits()))
    }

    /// Fetches a snapshot of the channel's state for debugging purposes.
    pub fn snapshot(&self) -> impl fmt::Debug + Clone {
        self.raw.snapshot()
    }

    pub fn bind(self, mask: S) -> BoundSignalChannel {
        BoundSignalChannel::wrap(self, mask)
    }

    pub fn bind_ref(&self, mask: S) -> BoundSignalChannel {
        self.clone().bind(mask)
    }
}

// === BoundSignalChannel === //

#[derive(Debug, Clone, Hash, Eq, PartialEq)]
pub struct BoundSignalChannel {
    pub channel: RawSignalChannel,
    pub mask: u64,
}

impl BoundSignalChannel {
    pub fn wrap<S: Flags<Bits = u64>>(channel: SignalChannel<S>, mask: S) -> Self {
        Self {
            channel: channel.into_raw(),
            mask: mask.bits(),
        }
    }

    pub fn assert(&self) {
        self.channel.assert(self.mask);
    }
}

// === Extensions === //

// Core
pub trait AnySignalChannel: Sized {
    type Mask: Copy;

    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R>;
}

impl AnySignalChannel for RawSignalChannel {
    type Mask = u64;

    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        // inherent impls take priority during name resolution
        self.wait(wake_mask, waker, worker)
    }
}

impl<S: Flags<Bits = u64> + Copy> AnySignalChannel for SignalChannel<S> {
    type Mask = S;

    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        // inherent impls take priority during name resolution
        self.wait(wake_mask, waker, worker)
    }
}

// Idle
pub trait ParkSignalChannelExt: AnySignalChannel {
    fn wait_on_park(&self, wake_mask: Self::Mask) {
        thread_local! {
            // We use our own parker implementation to avoid wake-ups from spurious
            // unpark operations.
            static MY_PARKER: Parker = Parker::default();
        }

        MY_PARKER.with(|parker| {
            self.wait(wake_mask, || parker.unpark(), || parker.park());
        });
    }
}

impl<T: AnySignalChannel> ParkSignalChannelExt for T {}

// === Tests === //

#[cfg(test)]
mod tests {
    use std::sync::Barrier;

    use crate::{ParkSignalChannelExt, RawSignalChannel};

    #[test]
    fn simple_wake_up() {
        let start_barrier = Barrier::new(2);
        let channel = RawSignalChannel::new();

        std::thread::scope(|s| {
            s.spawn(|| {
                start_barrier.wait();
                channel.wait_on_park(u64::MAX);
                assert_eq!(channel.take(1), 1);
            });

            s.spawn(|| {
                start_barrier.wait();
                channel.assert(1);
            });
        });
    }
}
