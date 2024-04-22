// N.B. if you decide to optimize this module, I strongly encourage you to keep this existing naïve
// implementation as a debugging fallback.

use std::{fmt, hash, marker::PhantomData, panic::Location, ptr::NonNull, sync::Arc};

use bitflags::Flags;
use parking_lot::Mutex;
use scopeguard::guard;

// === RawSignalChannel === //

#[derive(Clone, Default)]
pub struct RawSignalChannel {
    // TODO: move out `handler` and `wake_mask` for efficiency reasons.
    state: Arc<Mutex<RawSignalChannelInner>>,
}

#[derive(Default)]
struct RawSignalChannelInner {
    // The set of all asserted bits.
    asserted_mask: u64,

    // The set of all bits listened for by the current waker.
    wake_mask: u64,

    // Only valid if `wake_mask` is not zero.
    handler: Option<NonNull<dyn FnMut(u64) + Send + Sync>>,

    // For debugging purposes
    handler_location: Option<&'static Location<'static>>,
}

#[derive(Debug, Copy, Clone)]
pub struct RawSignalChannelSnapshot {
    pub asserted_mask: u64,
    pub wake_mask: u64,
    pub handler: Option<&'static Location<'static>>,
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
        Arc::as_ptr(&self.state).hash(state);
    }
}

impl Eq for RawSignalChannel {}

impl PartialEq for RawSignalChannel {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.state, &other.state)
    }
}

impl RawSignalChannel {
    pub fn new() -> Self {
        Self::default()
    }

    /// Asserts zero or more signals.
    pub fn assert(&self, mask: u64) {
        let mut state = self.state.lock();

        state.asserted_mask |= mask;
        if state.wake_mask & mask != 0 {
            // (implies that state.wake_mask != 0)
            unsafe { (*state.handler.unwrap().as_ptr())(mask) }
        }
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
        wake_mask: u64,
        waker: impl FnMut(u64) + Send + Sync,
        runner: impl FnOnce() -> R,
    ) -> Option<R> {
        // Unsize the waker
        let mut waker = waker;
        let waker = &mut waker as &mut (dyn '_ + FnMut(u64) + Send + Sync);

        // Bind the waker
        let mut state = self.state.lock();

        assert_eq!(
            state.handler, None,
            "`open` already called somewhere else on this channel"
        );

        if state.asserted_mask & wake_mask != 0 {
            return None;
        }

        #[allow(clippy::unnecessary_cast)] // false positive
        {
            state.handler_location = Some(Location::caller());
            state.handler = NonNull::new(
                waker as *mut (dyn '_ + FnMut(u64) + Send + Sync)
                    as *mut (dyn FnMut(u64) + Send + Sync),
            );
        }

        state.wake_mask = wake_mask;
        drop(state);

        // Run the task with a guard to clear the waker before we invalidate it by leaving this
        // function.
        let _guard = guard((), |()| {
            let mut state = self.state.lock();
            // Mark the handler as invalid so people don't try to wake us up with a dead handler.
            state.wake_mask = 0;
            state.handler = None;
            state.handler_location = None;
        });

        Some(runner())
    }

    /// Takes all signals under the specified mask, clearing them in the process.
    pub fn take(&self, mask: u64) -> u64 {
        let mut state = self.state.lock();

        let taken = state.asserted_mask & mask;
        state.asserted_mask &= !mask;
        taken
    }

    /// Fetches a snapshot of the channel's state for debugging purposes.
    pub fn snapshot(&self) -> RawSignalChannelSnapshot {
        let state = self.state.lock();

        RawSignalChannelSnapshot {
            asserted_mask: state.asserted_mask,
            wake_mask: state.wake_mask,
            handler: state.handler_location,
        }
    }
}

// === SignalChannel === //

pub struct SignalChannel<S> {
    _ty: PhantomData<fn() -> S>,
    raw: RawSignalChannel,
}

#[derive(Debug, Copy, Clone)]
pub struct SignalChannelSnapshot<S> {
    pub asserted_mask: S,
    pub wake_mask: S,
    pub handler: Option<&'static Location<'static>>,
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
        mut waker: impl FnMut(S) + Send + Sync,
        runner: impl FnOnce() -> R,
    ) -> Option<R> {
        self.raw.wait(
            wake_mask.bits(),
            move |bits| waker(S::from_bits_retain(bits)),
            runner,
        )
    }

    /// Honors all signals under the specified mask, clearing them in the process.
    pub fn take(&self, mask: S) -> S {
        S::from_bits_retain(self.raw.take(mask.bits()))
    }

    /// Fetches a snapshot of the channel's state for debugging purposes.
    pub fn snapshot(&self) -> SignalChannelSnapshot<S> {
        let snap = self.raw.snapshot();

        SignalChannelSnapshot {
            asserted_mask: S::from_bits_retain(snap.asserted_mask),
            wake_mask: S::from_bits_retain(snap.wake_mask),
            handler: snap.handler,
        }
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
        waker: impl FnMut(Self::Mask) + Send + Sync,
        runner: impl FnOnce() -> R,
    ) -> Option<R>;
}

impl AnySignalChannel for RawSignalChannel {
    type Mask = u64;

    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnMut(Self::Mask) + Send + Sync,
        runner: impl FnOnce() -> R,
    ) -> Option<R> {
        // inherent impls take priority during name resolution
        self.wait(wake_mask, waker, runner)
    }
}

impl<S: Flags<Bits = u64> + Copy> AnySignalChannel for SignalChannel<S> {
    type Mask = S;

    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnMut(Self::Mask) + Send + Sync,
        runner: impl FnOnce() -> R,
    ) -> Option<R> {
        // inherent impls take priority during name resolution
        self.wait(wake_mask, waker, runner)
    }
}

// Idle
pub trait ParkSignalChannelExt: AnySignalChannel {
    fn wait_on_park(&self, wake_mask: Self::Mask) {
        let thread = std::thread::current();
        self.wait(wake_mask, |_| thread.unpark(), std::thread::park);
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
