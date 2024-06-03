use std::{any::TypeId, fmt, marker::PhantomData, ops::Deref, sync::Arc};

use bitflags::Flags;
use memmage::CastableRef;

use crate::{
    util::{FmtDebugUsingDisplay, FmtU64AsBits},
    Waker, WakerIndex, WakerSet, WakerSetHas,
};

#[cfg(not(loom))]
use std::sync::atomic::{fence, AtomicU32, AtomicU64, Ordering::*};

#[cfg(loom)]
use loom::sync::atomic::{fence, AtomicU32, AtomicU64, Ordering::*};

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

// === AnySignalChannel === //

pub trait AnySignalChannel: Sized {
    type WakerSet: WakerSet;
    type Mask: Copy;

    fn raw(&self) -> &RawSignalChannel<Self::WakerSet>;

    fn mask_to_u64(mask: Self::Mask) -> u64;
}

pub trait AnySignalChannelWith<T: Waker>: AnySignalChannel<WakerSet = Self::WakerSet2> {
    type WakerSet2: WakerSetHas<T>;
}

impl<W: WakerSet> AnySignalChannel for RawSignalChannel<W> {
    type WakerSet = W;
    type Mask = u64;

    fn raw(&self) -> &RawSignalChannel<Self::WakerSet> {
        self
    }

    fn mask_to_u64(mask: Self::Mask) -> u64 {
        mask
    }
}

impl<T, W> AnySignalChannelWith<T> for RawSignalChannel<W>
where
    T: Waker,
    W: WakerSetHas<T>,
{
    type WakerSet2 = W;
}

impl<S, W> AnySignalChannel for SignalChannel<S, W>
where
    W: WakerSet,
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

impl<T, S, W> AnySignalChannelWith<T> for SignalChannel<S, W>
where
    T: Waker,
    W: WakerSetHas<T>,
    S: Copy + Flags<Bits = u64>,
{
    type WakerSet2 = W;
}

// === BoundSignalChannel === //

pub type BoundSignalChannelRef<'a> = BoundSignalChannel<&'a RawSignalChannel>;

pub type ArcBoundSignalChannel = BoundSignalChannel<Arc<RawSignalChannel>>;

#[derive(Debug, Clone, Hash, Eq, PartialEq)]
pub struct BoundSignalChannel<P> {
    pub channel: P,
    pub mask: u64,
}

impl<P> BoundSignalChannel<P>
where
    P: Deref<Target = RawSignalChannel>,
{
    pub fn new<'p, P2>(channel: P2, mask: <P2::Target as AnySignalChannel>::Mask) -> Self
    where
        P2: CastableRef<'p, WithPointee<RawSignalChannel> = P>,
        P2::Target: AnySignalChannel,
    {
        Self {
            channel: channel.map(|v| v.raw() as &RawSignalChannel),
            mask: <P2::Target as AnySignalChannel>::mask_to_u64(mask),
        }
    }

    pub fn assert(&self) {
        self.channel.assert(self.mask);
    }

    pub fn take(&self) {
        self.channel.take(self.mask);
    }
}
