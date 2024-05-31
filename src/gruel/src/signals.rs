use std::{
    any::{Any, TypeId},
    fmt,
    marker::PhantomData,
    ops::Deref,
    ptr::NonNull,
    sync::{
        atomic::{AtomicBool, Ordering::*},
        Arc,
    },
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

    fn index_of(&self, ty: TypeId) -> Option<u32>;

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

    pub const fn index(self) -> u32 {
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

            fn index_of(
                &self,
                ty: $crate::define_waker_set_internal::TypeId,
            ) -> $crate::define_waker_set_internal::Option<$crate::define_waker_set_internal::u32> {
                $(if ty == $crate::define_waker_set_internal::TypeId::of::<$f_ty>() {
                    return $crate::define_waker_set_internal::Option::Some(Self::$f_name.index());
                })*

                $crate::define_waker_set_internal::Option::None
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

pub struct RawSignalChannel<W: ?Sized + WakerSet = dyn WakerSet> {
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

    pub fn wakers(&self) -> &W {
        &self.wakers
    }

    pub fn opt_waker_index<T: Waker>(&self) -> Option<WakerIndex<W>> {
        self.wakers
            .index_of(TypeId::of::<T>())
            .map(WakerIndex::new_unchecked)
    }

    pub fn opt_waker_state<T: Waker>(&self) -> Option<&T> {
        self.wakers
            .state_of(TypeId::of::<T>())
            .map(|v| v.downcast_ref().unwrap())
    }

    pub fn opt_waker<T: Waker>(&self) -> Option<(&T, WakerIndex<W>)> {
        self.opt_waker_state::<T>()
            .map(|state| (state, self.opt_waker_index::<T>().unwrap()))
    }

    pub fn waker_state<T>(&self) -> &T
    where
        T: Waker,
        W: WakerSetHas<T>,
    {
        self.opt_waker_state().unwrap()
    }
}

// This mechanism offers the following guarantees:
//
// 1. If a given signal in a `wake_up_mask` is asserted, the `wait` command will be skipped and/or the
//    waker will be called.
//
// 2. A given waker be called at most once for a given `wait` command.
//
// Unfortunately, the following guarantees are *not* yet made:
//
// 1. It is possible that a waker will be called and a task will be skipped at the same time. This
//    could potentially be fixed but doing so isn't too useful considering limitation 2.
//
// 2. It is possible that a wake call from an earlier `wait` can be so delayed that it ends up being
//    called in the context of a subsequent `wait`. This cannot be prevented without blocking the
//    `wait` call on completion of its wakers.
//
impl<W: ?Sized + WakerSet> RawSignalChannel<W> {
    pub fn wait_manual(
        &self,
        wake_up_mask: u64,
        abort_mask: u64,
        waker: WakerIndex<W>,
    ) -> WaitManualResult<'_> {
        debug_assert_eq!(self.active_waker.load(Relaxed), u32::MAX);

        self.active_waker.store(waker.index(), Relaxed);
        self.wake_up_mask.store(wake_up_mask, Relaxed);

        let wait_guard = SignalChannelWaitGuard {
            active_waker: &self.active_waker,
        };

        fence(SeqCst);

        // We know that this satisfies property 1 of the impl docs because if this branch is not taken
        // but `assert` is called in a way compatible with `wake_up_mask`, the `asserted_mask` must
        // not have been updated yet. We know that, once the `assert` call gets past the `asserted_mask`
        // update and the fence right after that, our updates to `active_waker` and `wake_up_mask`
        // must have been made visible to it. Hence, it will realize that it must wake-up.
        //
        // Hence, at a minimum, the waker will be called.
        let observed_mask = self.asserted_mask.load(Relaxed);
        if observed_mask & abort_mask != 0 {
            return WaitManualResult {
                observed_mask,
                wait_guard: None,
            };
        }

        WaitManualResult {
            observed_mask,
            wait_guard: Some(wait_guard),
        }
    }

    pub fn wait<R>(
        &self,
        mask: u64,
        waker: WakerIndex<W>,
        worker: impl FnOnce() -> R,
    ) -> Option<R> {
        let _undo_guard = self.wait_manual(mask, mask, waker).wait_guard?;

        Some(worker())
    }

    pub fn assert(&self, mask: u64) {
        let asserted_mask = self.asserted_mask.fetch_or(mask, Relaxed);

        fence(SeqCst);

        // Ensure that we only call the waker once for a given `wait` command. We know will be the
        // case since, before we unset the `waker`—thereby completing the uniqueness period—we will
        // have saturated the `asserted_mask` with an assertion bit that causes this branch to be
        // taken. Note that `take`'s updates to `asserted_mask` must be made visible after the waker
        // is unset to keep this true.
        if asserted_mask & self.wake_up_mask.load(Relaxed) != 0 {
            return;
        }

        let waker = self.active_waker.load(Relaxed);

        if waker != u32::MAX {
            self.wakers.wake(waker);
        }
    }

    pub fn take(&self, mask: u64) -> u64 {
        // Ensures that any threads which see the reduced assertion mask also see the `active_waker`,
        // which `wait` must have set to `u32::MAX`. This ensures that the waker cannot be called
        // more times than is expected. We use a fence here because, honestly, I'm not sure how the
        // other orderings would interact here.
        fence(SeqCst);

        self.asserted_mask.fetch_and(!mask, Relaxed) & mask
    }

    pub fn could_take(&self, mask: u64) -> bool {
        // (mimics `take`)
        fence(SeqCst);

        self.asserted_mask.load(Relaxed) & mask != 0
    }
}

#[derive(Debug)]
pub struct WaitManualResult<'a> {
    pub observed_mask: u64,
    pub wait_guard: Option<SignalChannelWaitGuard<'a>>,
}

#[derive(Debug)]
pub struct SignalChannelWaitGuard<'a> {
    active_waker: &'a AtomicU32,
}

impl Drop for SignalChannelWaitGuard<'_> {
    fn drop(&mut self) {
        self.active_waker.store(u32::MAX, Relaxed);
    }
}

// === SignalChannel === //

#[repr(transparent)]
pub struct SignalChannel<S, W: ?Sized + WakerSet = dyn WakerSet> {
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

    pub fn could_take(&self, mask: S) -> bool {
        self.raw.could_take(mask.bits())
    }
}

// === BoundSignalChannel === //

pub type BoundSignalChannelRef<'a> = BoundSignalChannel<&'a RawSignalChannel>;

#[derive(Debug, Clone, Hash, Eq, PartialEq)]
pub struct BoundSignalChannel<P> {
    pub channel: P,
    pub mask: u64,
}

impl<P> BoundSignalChannel<P>
where
    P: Deref<Target = RawSignalChannel>,
{
    pub fn assert(&self) {
        self.channel.assert(self.mask);
    }

    pub fn take(&self) {
        self.channel.take(self.mask);
    }
}

// === Arc Helpers === //

pub type ArcBoundSignalChannel = BoundSignalChannel<Arc<RawSignalChannel>>;

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
    type Ptr: Deref<Target = RawSignalChannel>;

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
    type Ptr = Arc<RawSignalChannel>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self.into_raw(),
            mask: mask.bits(),
        }
    }
}

impl<S> SignalChannelBindExt for Arc<SignalChannel<S>>
where
    S: Flags<Bits = u64>,
{
    type Mask = S;
    type Ptr = Arc<RawSignalChannel>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self.into_raw(),
            mask: mask.bits(),
        }
    }
}

impl<W: WakerSet> SignalChannelBindExt for Arc<RawSignalChannel<W>> {
    type Mask = u64;
    type Ptr = Arc<RawSignalChannel>;

    fn bind(self, mask: Self::Mask) -> BoundSignalChannel<Self::Ptr> {
        BoundSignalChannel {
            channel: self,
            mask,
        }
    }
}

impl SignalChannelBindExt for Arc<RawSignalChannel> {
    type Mask = u64;
    type Ptr = Arc<RawSignalChannel>;

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

        // Unfortunately, a given `wait` command is allowed to be both skipped and awaken, which can
        // easily lead in a left-over wake-up ticket. Hence, we loop until we're sure that the operation
        // has actually been woken up. In practice, this loop should very rarely be taken.
        while !raw.could_take(mask) {
            raw.wait(mask, WakerIndex::of::<ParkWaker>(), || {
                raw.waker_state::<ParkWaker>().0.park();
            });
        }
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

impl DynamicallyBoundWaker {
    #[allow(clippy::missing_safety_doc)]
    pub unsafe fn bind_waker(&self, waker: impl FnOnce() + Send + Sync) {
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

        let mut curr_waker = self.waker.lock();
        assert!(curr_waker.is_none());

        *curr_waker = Some(waker);
    }

    pub fn clear_waker(&self) {
        *self.waker.lock() = None;
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

        // Provide it to the interned waker
        let dyn_state = raw.waker_state::<DynamicallyBoundWaker>();
        unsafe { dyn_state.bind_waker(waker) };

        // Bind an undo scope guard
        let _undo_guard = scopeguard::guard((), |()| {
            dyn_state.clear_waker();
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

// === Signal Multiplexing === //

pub fn process_signals_multiplexed(
    handlers: &[&dyn SignalMultiplexHandler],
    should_stop: &AtomicBool,
    park: impl Fn(),
    unpark: impl Sync + Fn(),
) {
    // Create a bitflag for quickly determining which handlers are dirty.
    let dirty_flags = Box::from_iter((0..(handlers.len() + 63) / 64).map(|_| AtomicU64::new(0)));
    let dirty_flags = &*dirty_flags;

    // Create a parker for this subscriber loop.
    let unpark = &unpark;

    // Bind the subscriber to every handler.
    let mut wait_guards = Vec::new();

    for (i, handler) in handlers.iter().enumerate() {
        // Determine the bit in the dirty mask that this handler occupies.
        let slot_idx = i / 64;
        let slot_mask = 1 << (i % 64);
        let slot = &dirty_flags[slot_idx];

        // Bind each signal to the slot.
        for signal in handler.signals() {
            let (state, waiter_idx) = signal
                .opt_waker::<DynamicallyBoundWaker>()
                .expect("only signals with a `DynamicallyBoundWaker` can be multiplexed");

            // Set the waker's dynamic waking closure. We could theoretically do better than a
            // `DynamicallyBoundWaker` with some clever reference-counting but I don't really want
            // to implement such a complex system for such a performance-insensitive system.
            unsafe {
                // We have to be *very* careful to only borrow things that only expire after all the
                // wait guards are gone.
                state.bind_waker(move || {
                    slot.fetch_or(slot_mask, Relaxed);
                    (unpark)();
                });
            }

            let wait_result = signal.wait_manual(u64::MAX, 0, waiter_idx);
            if wait_result.observed_mask != 0 {
                slot.fetch_or(slot_mask, Relaxed);
            }

            let undo_waker_guard = wait_result.wait_guard;
            let unbind_dynamic_guard = scopeguard::guard((), |()| {
                state.clear_waker();
            });
            wait_guards.push((undo_waker_guard, unbind_dynamic_guard));
        }
    }

    // Process events
    while !should_stop.load(Relaxed) {
        for (i_cell, flag) in dirty_flags.iter().enumerate() {
            let mut flag = flag.swap(0, Relaxed);
            while flag != 0 {
                let i_bit = flag.trailing_zeros() as usize;
                flag ^= 1 << i_bit;
                let i = i_cell * 64 + i_bit;

                handlers[i].process();
            }
        }

        // Although we don't redo another `wait` operation here, this routine is still guaranteed not
        // to miss any events because `park` holds unpark tickets.
        (park)();
    }
}

pub trait SignalMultiplexHandler: 'static {
    fn process(&self);

    fn signals(&self) -> Vec<&RawSignalChannel>;
}

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
