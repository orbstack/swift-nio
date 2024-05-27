use std::{
    any::{Any, TypeId},
    fmt,
    marker::PhantomData,
    ops::Deref,
    ptr::NonNull,
    sync::{atomic::Ordering::*, Arc},
    time::Duration,
};

#[cfg(not(loom))]
use std::sync::atomic::{fence, AtomicU32, AtomicU64};

#[cfg(loom)]
use loom::sync::atomic::{fence, AtomicU32, AtomicU64};

use bitflags::Flags;
use derive_where::derive_where;
use parking_lot::Mutex;
use thiserror::Error;

use crate::util::{cast_arc, ExtensionFor, FmtDebugUsingDisplay, FmtU64AsBits, Parker};

// === WakerSet === //

// Traits
pub trait Waker: 'static + Send + Sync {
    fn wake(&self);
}

pub trait WakerSet: 'static + Send + Sync {
    fn wake(&self, index: u32);

    fn state_of(&self, ty: TypeId) -> Option<&(dyn Any + Send + Sync)>;

    fn name_of(&self, index: u32) -> &'static str;

    fn name_of_static(index: u32) -> &'static str
    where
        Self: Sized;
}

pub trait WakerSetHas<T: Waker>: Sized + WakerSet {
    const INDEX: WakerIndex<Self>;
}

#[derive_where(Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct WakerIndex<S: ?Sized + WakerSet> {
    _ty: PhantomData<fn(S) -> S>,
    index: u32,
}

impl<S: ?Sized + WakerSet> fmt::Debug for WakerIndex<S> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("WakerIndex").field(&self.index).finish()
    }
}

impl<S: ?Sized + WakerSet> WakerIndex<S> {
    pub const fn of<V>() -> Self
    where
        V: Waker,
        S: WakerSetHas<V>,
    {
        S::INDEX
    }

    pub const fn new_unchecked(index: u32) -> Self {
        Self {
            _ty: PhantomData,
            index,
        }
    }

    pub fn index(self) -> u32 {
        self.index
    }
}

// `define_waker_set!`
#[doc(hidden)]
pub mod define_waker_set_internal {
    pub use {
        super::{Waker, WakerIndex, WakerSet, WakerSetHas},
        std::{
            any::{type_name, Any, TypeId},
            marker::{Send, Sized, Sync},
            option::Option,
            primitive::{str, u32},
            unreachable,
        },
    };
}

#[macro_export]
macro_rules! define_waker_set {
    ($(
        $(#[$attr:meta])*
        $vis:vis struct $name:ident {
            $(
                $(#[$f_attr:meta])*
                $f_vis:vis $f_name:ident: $f_ty:ty
            ),*
            $(,)?
        }
    )*) => {$(
        $(#[$attr])*
        $vis struct $name {
            $(
                $(#[$f_attr])*
                $f_vis $f_name: $f_ty,
            )*
        }

        #[allow(non_upper_case_globals)]
        impl $name {
            $crate::define_waker_set!(@internal; 0, $($f_name)*);
        }

        $(impl $crate::define_waker_set_internal::WakerSetHas<$f_ty> for $name {
            const INDEX: $crate::define_waker_set_internal::WakerIndex<Self> = Self::$f_name;
        })*

        impl $crate::define_waker_set_internal::WakerSet for $name {
            fn wake(&self, index: $crate::define_waker_set_internal::u32) {
                $(if index == Self::$f_name.index() {
                    $crate::define_waker_set_internal::Waker::wake(&self.$f_name);
                })*
            }

            fn state_of(
                &self,
                ty: $crate::define_waker_set_internal::TypeId,
            ) -> $crate::define_waker_set_internal::Option<&(
                dyn $crate::define_waker_set_internal::Any +
                    $crate::define_waker_set_internal::Send +
                    $crate::define_waker_set_internal::Sync
            )> {
                $(if ty == $crate::define_waker_set_internal::TypeId::of::<$f_ty>() {
                    return $crate::define_waker_set_internal::Option::Some(&self.$f_name);
                })*

                $crate::define_waker_set_internal::Option::None
            }

            fn name_of(&self, index: $crate::define_waker_set_internal::u32) -> &'static $crate::define_waker_set_internal::str {
                Self::name_of_static(index)
            }

            fn name_of_static(index: $crate::define_waker_set_internal::u32) -> &'static $crate::define_waker_set_internal::str {
                $(if index == Self::$f_name.index() {
                    return $crate::define_waker_set_internal::type_name::<$f_ty>();
                })*

                "<no waker set>"
            }
        }
    )*};
    (@internal; $counter:expr, $first:ident $($name:ident)*) => {
        pub const $first: $crate::define_waker_set_internal::WakerIndex<Self> =
            $crate::define_waker_set_internal::WakerIndex::new_unchecked(
                $counter
            );

        $crate::define_waker_set!(@internal; $counter + 1, $($name)*);
    };
    (@internal; $counter:expr,) => {};
}

// === RawSignalChannel === //

pub struct RawSignalChannel<W: ?Sized + WakerSet> {
    asserted_mask: AtomicU64,
    wake_up_mask: AtomicU64,
    active_waker: AtomicU32,
    wakers: W,
}

impl<W: ?Sized + WakerSet> fmt::Debug for RawSignalChannel<W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("RawSignalChannel")
            .field(
                "asserted_mask",
                &FmtU64AsBits(self.asserted_mask.load(Relaxed)),
            )
            .field(
                "active_waker",
                &FmtDebugUsingDisplay(self.wakers.name_of(self.active_waker.load(Relaxed))),
            )
            .finish()
    }
}

impl<W: ?Sized + WakerSet> RawSignalChannel<W> {
    pub fn new(wakers: W) -> Self
    where
        W: Sized,
    {
        Self {
            asserted_mask: AtomicU64::new(0),
            wake_up_mask: AtomicU64::new(0),
            active_waker: AtomicU32::new(u32::MAX),
            wakers,
        }
    }

    pub fn opt_waker_state<T: Waker>(&self) -> Option<&T> {
        self.wakers
            .state_of(TypeId::of::<T>())
            .map(|v| v.downcast_ref().unwrap())
    }

    pub fn waker_state<T>(&self) -> &T
    where
        T: Waker,
        W: WakerSetHas<T>,
    {
        self.opt_waker_state().unwrap()
    }

    pub fn wait<R>(
        &self,
        wake_up_mask: u64,
        waker: WakerIndex<W>,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        debug_assert_eq!(self.active_waker.load(Relaxed), u32::MAX);

        self.active_waker.store(waker.index(), Relaxed);
        self.wake_up_mask.store(wake_up_mask, Relaxed);

        let _undo_guard = scopeguard::guard((), |()| {
            self.active_waker.store(u32::MAX, Relaxed);
        });

        fence(SeqCst);

        if self.asserted_mask.load(Relaxed) != 0 {
            return None;
        }

        Some(worker())
    }

    pub fn assert(&self, mask: u64) {
        if self.asserted_mask.fetch_or(mask, Relaxed) & self.wake_up_mask.load(Relaxed) != 0 {
            return;
        }

        fence(SeqCst);

        let waker = self.active_waker.load(Relaxed);

        if waker != u32::MAX {
            self.wakers.wake(waker);
        }
    }

    pub fn take(&self, mask: u64) -> u64 {
        self.asserted_mask.fetch_and(!mask, Relaxed) & mask
    }
}

// === SignalChannel === //

#[repr(transparent)]
pub struct SignalChannel<S, W: ?Sized + WakerSet> {
    _ty: PhantomData<fn(S) -> S>,
    raw: RawSignalChannel<W>,
}

impl<S, W> fmt::Debug for SignalChannel<S, W>
where
    S: fmt::Debug + Flags<Bits = u64>,
    W: ?Sized + WakerSet,
{
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("SignalChannel")
            .field(
                "asserted_mask",
                &S::from_bits_retain(self.raw.asserted_mask.load(Relaxed)),
            )
            .field(
                "active_waker",
                &FmtDebugUsingDisplay(self.raw.wakers.name_of(self.raw.active_waker.load(Relaxed))),
            )
            .finish()
    }
}

impl<S, W> SignalChannel<S, W>
where
    W: ?Sized + WakerSet,
{
    pub fn new(wakers: W) -> Self
    where
        W: Sized,
    {
        Self::from_raw(RawSignalChannel::new(wakers))
    }

    pub fn from_raw(raw: RawSignalChannel<W>) -> Self
    where
        W: Sized,
    {
        Self {
            _ty: PhantomData,
            raw,
        }
    }

    pub fn raw(&self) -> &RawSignalChannel<W> {
        &self.raw
    }

    pub fn into_raw(self) -> RawSignalChannel<W>
    where
        W: Sized,
    {
        self.raw
    }

    pub fn opt_waker_state<T: Waker>(&self) -> Option<&T> {
        self.raw.opt_waker_state()
    }

    pub fn waker_state<T>(&self) -> &T
    where
        T: Waker,
        W: WakerSetHas<T>,
    {
        self.raw.waker_state()
    }
}

impl<S, W> SignalChannel<S, W>
where
    S: Flags<Bits = u64>,
    W: ?Sized + WakerSet,
{
    pub fn wait<R>(&self, mask: S, waker: WakerIndex<W>, worker: impl FnOnce() -> R) -> Option<R> {
        self.raw.wait(mask.bits(), waker, worker)
    }

    pub fn assert(&self, mask: S) {
        self.raw.assert(mask.bits())
    }

    pub fn take(&self, mask: S) -> S {
        S::from_bits_retain(self.raw.take(mask.bits()))
    }
}

// === BoundSignalChannel === //

#[derive(Debug, Clone, Hash, Eq, PartialEq)]
pub struct BoundSignalChannel<P> {
    pub channel: P,
    pub mask: u64,
}

impl<P> BoundSignalChannel<P>
where
    P: Deref<Target = RawSignalChannel<dyn WakerSet>>,
{
    pub fn assert(&self) {
        self.channel.assert(self.mask);
    }

    pub fn take(&self) {
        self.channel.take(self.mask);
    }
}

// === Arc Helpers === //

pub type ArcBoundSignalChannel = BoundSignalChannel<Arc<RawSignalChannel<dyn WakerSet>>>;

pub trait ArcSignalChannelExt:
    ExtensionFor<Arc<SignalChannel<Self::Signal, Self::WakerSet>>>
{
    type Signal;
    type WakerSet: ?Sized + WakerSet;

    fn into_raw(self) -> Arc<RawSignalChannel<Self::WakerSet>>;
}

impl<S, W> ArcSignalChannelExt for Arc<SignalChannel<S, W>>
where
    W: ?Sized + WakerSet,
{
    type Signal = S;
    type WakerSet = W;

    fn into_raw(self) -> Arc<RawSignalChannel<Self::WakerSet>> {
        cast_arc(self, |v| v.raw())
    }
}

pub trait SignalChannelBindExt: Clone {
    type Mask;
    type Ptr: Deref<Target = RawSignalChannel<dyn WakerSet>>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr>;

    fn bind_clone(&self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        self.clone().bind(mask)
    }
}

impl<S, W> SignalChannelBindExt for Arc<SignalChannel<S, W>>
where
    S: Flags<Bits = u64>,
    W: WakerSet,
{
    type Mask = S;
    type Ptr = Arc<RawSignalChannel<dyn WakerSet>>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self.into_raw(),
            mask: mask.bits(),
        }
    }
}

impl<S> SignalChannelBindExt for Arc<SignalChannel<S, dyn WakerSet>>
where
    S: Flags<Bits = u64>,
{
    type Mask = S;
    type Ptr = Arc<RawSignalChannel<dyn WakerSet>>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self.into_raw(),
            mask: mask.bits(),
        }
    }
}

impl<W: WakerSet> SignalChannelBindExt for Arc<RawSignalChannel<W>> {
    type Mask = u64;
    type Ptr = Arc<RawSignalChannel<dyn WakerSet>>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self,
            mask,
        }
    }
}

impl SignalChannelBindExt for Arc<RawSignalChannel<dyn WakerSet>> {
    type Mask = u64;
    type Ptr = Arc<RawSignalChannel<dyn WakerSet>>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self,
            mask,
        }
    }
}

// === Extensions === //

// Helper traits
pub trait AnySignalChannel<T: Waker>: Sized {
    type WakerSet: WakerSetHas<T>;
    type Mask: Copy;

    fn raw(&self) -> &RawSignalChannel<Self::WakerSet>;

    fn mask_to_u64(mask: Self::Mask) -> u64;
}

impl<T, W> AnySignalChannel<T> for RawSignalChannel<W>
where
    T: Waker,
    W: WakerSetHas<T>,
{
    type WakerSet = W;
    type Mask = u64;

    fn raw(&self) -> &RawSignalChannel<Self::WakerSet> {
        self
    }

    fn mask_to_u64(mask: Self::Mask) -> u64 {
        mask
    }
}

impl<T, S, W> AnySignalChannel<T> for SignalChannel<S, W>
where
    T: Waker,
    W: WakerSetHas<T>,
    S: Copy + Flags<Bits = u64>,
{
    type WakerSet = W;
    type Mask = S;

    fn raw(&self) -> &RawSignalChannel<Self::WakerSet> {
        self.raw()
    }

    fn mask_to_u64(mask: Self::Mask) -> u64 {
        mask.bits()
    }
}

// Parker
#[derive(Debug, Default)]
pub struct ParkWaker(Parker);

impl Waker for ParkWaker {
    fn wake(&self) {
        self.0.unpark();
    }
}

pub trait ParkSignalChannelExt: AnySignalChannel<ParkWaker> {
    fn wait_on_park(&self, mask: Self::Mask) {
        let raw = self.raw();
        let mask = Self::mask_to_u64(mask);

        raw.wait(mask, WakerIndex::of::<ParkWaker>(), || {
            raw.waker_state::<ParkWaker>().0.park();
        });
    }

    fn wait_on_park_timeout(&self, mask: Self::Mask, timeout: Duration) {
        let raw = self.raw();
        let mask = Self::mask_to_u64(mask);

        raw.wait(mask, WakerIndex::of::<ParkWaker>(), || {
            raw.waker_state::<ParkWaker>().0.park_timeout(timeout);
        });
    }
}

impl<T: AnySignalChannel<ParkWaker>> ParkSignalChannelExt for T {}

// Dynamically Bound (or: "I heard y'all liked the convenient legacy API!")
#[derive(Default)]
pub struct DynamicallyBoundWaker {
    waker: Mutex<Option<NonNull<dyn FnMut() + Send + Sync>>>,
}

unsafe impl Send for DynamicallyBoundWaker {}

unsafe impl Sync for DynamicallyBoundWaker {}

impl Waker for DynamicallyBoundWaker {
    fn wake(&self) {
        if let Some(waker) = self.waker.lock().as_mut() {
            (unsafe { waker.as_mut() })()
        }
    }
}

pub trait DynamicallyBoundSignalChannelExt: AnySignalChannel<DynamicallyBoundWaker> {
    fn wait_on_closure<R>(
        &self,
        mask: Self::Mask,
        waker: impl FnOnce() + Send + Sync,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        let raw = self.raw();
        let mask = Self::mask_to_u64(mask);

        // Unsize the waker
        let mut waker = Some(waker);
        let mut waker = move || {
            if let Some(waker) = waker.take() {
                waker()
            }
        };

        let waker = unsafe {
            #[allow(clippy::unnecessary_cast)]
            NonNull::new_unchecked(
                &mut waker as *mut (dyn FnMut() + Send + Sync + '_)
                    as *mut (dyn FnMut() + Send + Sync),
            )
        };

        // Provide it to the interned waker
        let dyn_state = raw.waker_state::<DynamicallyBoundWaker>();

        {
            let mut curr_waker = dyn_state.waker.lock();
            assert!(curr_waker.is_none());

            *curr_waker = Some(waker);
        }

        // Bind an undo scope guard
        let _undo_guard = scopeguard::guard((), |()| {
            *dyn_state.waker.lock() = None;
        });

        // Run the actual wait operation
        raw.wait(mask, WakerIndex::of::<DynamicallyBoundWaker>(), worker)
    }
}

impl<T: AnySignalChannel<DynamicallyBoundWaker>> DynamicallyBoundSignalChannelExt for T {}

// Queue Receiving
#[derive(Debug, Copy, Clone, Eq, PartialEq, Error)]
pub enum QueueRecvError {
    #[error("queue senders have all disconnected")]
    HungUp,

    #[error("receive operation was cancelled")]
    Cancelled,
}

pub trait QueueRecvSignalChannelExt: DynamicallyBoundSignalChannelExt {
    #[track_caller]
    fn recv_with_cancel<T>(
        &self,
        mask: Self::Mask,
        receiver: &crossbeam_channel::Receiver<T>,
    ) -> Result<T, QueueRecvError> {
        enum Never {}

        let (cancel_send, cancel_recv) = crossbeam_channel::bounded::<Never>(0);

        self.wait_on_closure(
            mask,
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

impl<T: DynamicallyBoundSignalChannelExt> QueueRecvSignalChannelExt for T {}

// === Tests === //

#[cfg(all(test, not(loom)))]
mod tests {
    use std::{sync::Barrier, thread, time::Duration};

    use crate::{
        DynamicallyBoundWaker, ParkSignalChannelExt, ParkWaker, QueueRecvSignalChannelExt,
        RawSignalChannel,
    };

    define_waker_set! {
        #[derive(Default)]
        struct MyWakerSet {
            parker: ParkWaker,
            dynamic: DynamicallyBoundWaker,
        }
    }

    #[test]
    fn simple_wake_up() {
        let start_barrier = Barrier::new(2);
        let channel = RawSignalChannel::new(MyWakerSet::default());

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
        let signal = RawSignalChannel::new(MyWakerSet::default());

        signal.assert(1);

        for _ in 0..1000 {
            signal.wait_on_park(u64::MAX);
        }
    }

    #[test]
    fn respects_timeouts() {
        let signal = RawSignalChannel::new(MyWakerSet::default());
        signal.wait_on_park_timeout(u64::MAX, Duration::from_millis(100));
    }

    #[test]
    fn queues_can_be_cancelled() {
        let (_send, recv) = crossbeam_channel::unbounded::<u32>();
        let signal = RawSignalChannel::new(MyWakerSet::default());

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
        let signal = RawSignalChannel::new(MyWakerSet::default());

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

#[cfg(all(loom, test))]
mod loom_tests {
    use super::*;

    use std::sync::{Arc, Barrier};

    define_waker_set! {
        #[derive(Default)]
        struct MyWakerSet {
            parker: ParkWaker,
        }
    }

    #[test]
    fn single_wake_up_loom() {
        loom::model(|| {
            let channel = Arc::new(RawSignalChannel::new(MyWakerSet::default()));

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.wait_on_park(0b1);
                    assert_eq!(channel.take(u64::MAX), 0b1);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b1);
                }
            });
        });
    }

    #[test]
    fn double_wake_up_loom() {
        loom::model(|| {
            let channel = Arc::new(RawSignalChannel::new(MyWakerSet::default()));

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.wait_on_park(0b1);
                    assert_eq!(channel.take(0b1), 0b1);

                    channel.wait_on_park(0b10);
                    assert_eq!(channel.take(0b10), 0b10);

                    assert_eq!(channel.take(u64::MAX), 0);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b1);
                    channel.assert(0b10);
                }
            });
        });
    }

    #[test]
    fn multi_source_wake_up_loom() {
        loom::model(|| {
            let channel = Arc::new(RawSignalChannel::new(MyWakerSet::default()));

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.wait_on_park(0b1);
                    assert_eq!(channel.take(0b1), 0b1);

                    channel.wait_on_park(0b10);
                    assert_eq!(channel.take(0b10), 0b10);

                    assert_eq!(channel.take(u64::MAX), 0);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b1);
                }
            });

            loom::thread::spawn({
                let channel = channel.clone();

                move || {
                    channel.assert(0b10);
                }
            });
        });
    }
}
