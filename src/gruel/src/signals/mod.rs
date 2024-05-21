use std::{fmt, hash, marker::PhantomData, sync::Arc, time::Duration};

use bitflags::Flags;
use thiserror::Error;

use crate::util::Parker;

// === "Backends" === //

mod impl_dummy;
mod impl_naive;
mod impl_optimized;

use impl_optimized::RawSignalChannelInner;

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

    pub fn bind(self, mask: u64) -> BoundSignalChannel {
        BoundSignalChannel {
            channel: self,
            mask,
        }
    }

    pub fn bind_clone(&self, mask: u64) -> BoundSignalChannel {
        self.clone().bind(mask)
    }

    pub fn bind_ref(&self, mask: u64) -> BoundSignalChannelRef<'_> {
        BoundSignalChannelRef {
            channel: self,
            mask,
        }
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
        BoundSignalChannel {
            channel: self.into_raw(),
            mask: mask.bits(),
        }
    }

    pub fn bind_clone(&self, mask: S) -> BoundSignalChannel {
        self.clone().bind(mask)
    }

    pub fn bind_ref(&self, mask: S) -> BoundSignalChannelRef<'_> {
        BoundSignalChannelRef {
            channel: self.raw(),
            mask: mask.bits(),
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
    pub fn assert(&self) {
        self.channel.assert(self.mask);
    }

    pub fn take(&self) {
        self.channel.take(self.mask);
    }
}

// === BoundSignalChannelRef === //

#[derive(Debug, Clone, Hash, Eq, PartialEq)]
pub struct BoundSignalChannelRef<'a> {
    pub channel: &'a RawSignalChannel,
    pub mask: u64,
}

impl<'a> BoundSignalChannelRef<'a> {
    pub fn assert(&self) {
        self.channel.assert(self.mask);
    }

    pub fn take(&self) {
        self.channel.take(self.mask);
    }
}

// === Extensions === //

// Core
pub trait AnySignalChannel: Sized {
    type Mask: Copy;

    fn assert(&self, mask: Self::Mask);

    fn take(&self, mask: Self::Mask) -> Self::Mask;

    #[track_caller]
    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel;

    fn bind_clone(&self, mask: Self::Mask) -> BoundSignalChannel;

    fn bind_ref(&self, mask: Self::Mask) -> BoundSignalChannelRef<'_>;
}

impl AnySignalChannel for RawSignalChannel {
    type Mask = u64;

    fn assert(&self, mask: Self::Mask) {
        // inherent impls take priority during name resolution
        self.assert(mask)
    }

    fn take(&self, mask: Self::Mask) -> Self::Mask {
        // inherent impls take priority during name resolution
        self.take(mask)
    }

    #[track_caller]
    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        // inherent impls take priority during name resolution
        self.wait(wake_mask, waker, worker)
    }

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel {
        // inherent impls take priority during name resolution
        self.bind(mask)
    }

    fn bind_clone(&self, mask: Self::Mask) -> BoundSignalChannel {
        // inherent impls take priority during name resolution
        self.bind_clone(mask)
    }

    fn bind_ref(&self, mask: Self::Mask) -> BoundSignalChannelRef<'_> {
        // inherent impls take priority during name resolution
        self.bind_ref(mask)
    }
}

impl<S: Flags<Bits = u64> + Copy> AnySignalChannel for SignalChannel<S> {
    type Mask = S;

    fn assert(&self, mask: Self::Mask) {
        // inherent impls take priority during name resolution
        self.assert(mask)
    }

    fn take(&self, mask: Self::Mask) -> Self::Mask {
        // inherent impls take priority during name resolution
        self.take(mask)
    }

    #[track_caller]
    fn wait<R>(
        &self,
        wake_mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        // inherent impls take priority during name resolution
        self.wait(wake_mask, waker, worker)
    }

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel {
        // inherent impls take priority during name resolution
        self.bind(mask)
    }

    fn bind_clone(&self, mask: Self::Mask) -> BoundSignalChannel {
        // inherent impls take priority during name resolution
        self.bind_clone(mask)
    }

    fn bind_ref(&self, mask: Self::Mask) -> BoundSignalChannelRef<'_> {
        // inherent impls take priority during name resolution
        self.bind_ref(mask)
    }
}

// Idle
pub trait ParkSignalChannelExt: AnySignalChannel {
    #[track_caller]
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

    #[track_caller]
    fn wait_on_park_timeout(&self, wake_mask: Self::Mask, timeout: Duration) {
        thread_local! {
            // We use our own parker implementation to avoid wake-ups from spurious
            // unpark operations.
            static MY_PARKER: Parker = Parker::default();
        }

        MY_PARKER.with(|parker| {
            self.wait(
                wake_mask,
                || parker.unpark(),
                || parker.park_timeout(timeout),
            );
        });
    }
}

impl<T: AnySignalChannel> ParkSignalChannelExt for T {}

// Queue Receiving
#[derive(Debug, Copy, Clone, Eq, PartialEq, Error)]
pub enum QueueRecvError {
    #[error("queue senders have all disconnected")]
    HungUp,

    #[error("receive operation was cancelled")]
    Cancelled,
}

pub trait QueueRecvSignalChannelExt: AnySignalChannel {
    #[track_caller]
    fn recv_with_cancel<T>(
        &self,
        cancel_mask: Self::Mask,
        receiver: &crossbeam_channel::Receiver<T>,
    ) -> Result<T, QueueRecvError> {
        enum Never {}

        let (cancel_send, cancel_recv) = crossbeam_channel::bounded::<Never>(0);

        self.wait(
            cancel_mask,
            || drop(cancel_send),
            || {
                crossbeam_channel::select! {
                    recv(receiver) -> val => {
                        val.map_err(|_| QueueRecvError::HungUp)
                    }
                    recv(cancel_recv) -> _ => {
                        Err(QueueRecvError::Cancelled)
                    }
                }
            },
        )
        .unwrap_or(Err(QueueRecvError::Cancelled))
    }
}

impl<T: AnySignalChannel> QueueRecvSignalChannelExt for T {}

// Queue Communication

/// To handle events safely, it is necessary to implement the following pattern:
///
/// - Sender thread:
///    - Push to queue
///    - Assert signal
///
/// - Worker thread:
///    - De-assert signal
///    - Check if queue is empty
///
/// These functions help us do that!
///
/// To show why this is true, consider what happens in the worst-case-scenario, where the worker
/// thread observes an empty queue. This can only happen if the sender thread hasn't ran yet. Hence,
/// once the sender eventually finishes, the worker thread is condemned to receive the signal
/// whenever they start preemptible work again.
///
/// In the opposite order, the assertion and deassertion could be interwoven, leaving the push and
/// the check to unsafely race.
pub trait QueueCommSignalChannelExt: AnySignalChannel {
    fn add_to_queue_with_kick(&self, mask: Self::Mask, push_to_queue: impl FnOnce()) {
        push_to_queue();
        self.assert(mask);
    }

    fn check_queue_for_work_with_ack(
        &self,
        mask: Self::Mask,
        mut queue_has_work: impl FnMut() -> bool,
    ) -> bool {
        // Optimization: ensure that we have no work in the queue before acknowledging the signal to
        // reduce the amount of work the `assert` routine has to do. This is not needed for correctness.
        if queue_has_work() {
            return true;
        }

        self.take(mask);
        queue_has_work()
    }
}

// === Tests === //

#[cfg(test)]
mod tests {
    use std::{sync::Barrier, thread, time::Duration};

    use crate::{ParkSignalChannelExt, QueueRecvSignalChannelExt, RawSignalChannel};

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

    #[test]
    fn early_exit_works() {
        let signal = RawSignalChannel::new();

        signal.assert(1);

        for _ in 0..1000 {
            signal.wait_on_park(u64::MAX);
        }
    }

    #[test]
    fn respects_timeouts() {
        let signal = RawSignalChannel::new();
        signal.wait_on_park_timeout(0, Duration::from_millis(100));
    }

    #[test]
    fn queues_can_be_cancelled() {
        let (_send, recv) = crossbeam_channel::unbounded::<u32>();
        let signal = RawSignalChannel::new();

        thread::scope(|s| {
            s.spawn(|| {
                assert_eq!(
                    signal.recv_with_cancel(u64::MAX, &recv),
                    Err(crate::QueueRecvError::Cancelled)
                );
            });

            s.spawn(|| {
                // We'd like to exercise the cancellation behavior, ideally.
                thread::sleep(Duration::from_millis(100));
                signal.assert(1);
            });
        });
    }

    #[test]
    fn queues_can_still_receive() {
        let (send, recv) = crossbeam_channel::unbounded::<u32>();
        let signal = RawSignalChannel::new();

        thread::scope(|s| {
            s.spawn(|| {
                assert_eq!(signal.recv_with_cancel(u64::MAX, &recv), Ok(42));
            });

            s.spawn(|| {
                thread::sleep(Duration::from_millis(100));
                send.send(42).unwrap();
            });
        });
    }
}
