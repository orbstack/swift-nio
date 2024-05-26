use std::{
    fmt, hash,
    marker::PhantomData,
    sync::{
        atomic::{fence, AtomicU32, AtomicU64, Ordering::*},
        Arc,
    },
};

use derive_where::derive_where;

use crate::util::FmtDebugUsingDisplay;

// === WakerSet === //

// Traits
pub trait WakerSet: 'static + Send + Sync {
    fn wake(&self, index: u32);

    fn name_of(&self, index: u32) -> &'static str;
}

pub trait WakerSetHas<T: Waker>: Sized + WakerSet {
    const REF: WakerSetRef<Self>;
}

pub trait Waker: 'static + Send + Sync {
    fn wake(&self);
}

#[derive_where(Debug, Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct WakerSetRef<S: WakerSet> {
    _ty: PhantomData<fn(S) -> S>,
    index: u32,
}

impl<S: WakerSet> WakerSetRef<S> {
    pub const fn new<V>() -> Self
    where
        V: Waker,
        S: WakerSetHas<V>,
    {
        S::REF
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
        super::{Waker, WakerSet, WakerSetHas, WakerSetRef},
        std::{
            any::type_name,
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
            const REF: $crate::define_waker_set_internal::WakerSetRef<Self> = Self::$f_name;
        })*

        impl $crate::define_waker_set_internal::WakerSet for $name {
            fn wake(&self, index: $crate::define_waker_set_internal::u32) {
                $(if index == Self::$f_name.index() {
                    $crate::define_waker_set_internal::Waker::wake(&self.$f_name);
                })*
            }

            fn name_of(&self, index: $crate::define_waker_set_internal::u32) -> &'static $crate::define_waker_set_internal::str {
                $(if index == Self::$f_name.index() {
                    return $crate::define_waker_set_internal::type_name::<$f_type>();
                })*

                "<no waker set>"
            }
        }
    )*};
    (@internal; $counter:expr, $first:ident $($name:ident)*) => {
        pub const $first: $crate::define_waker_set_internal::WakerSetRef<Self> =
            $crate::define_waker_set_internal::WakerSetRef::new_unchecked(
                $counter
            );

        $crate::define_waker_set!(@internal; $counter + 1, $($name)*);
    };
    (@internal; $counter:expr,) => {};
}

// === RawSignalChannel === //

pub struct RawSignalChannel<W: ?Sized + WakerSet> {
    inner: Arc<RawSignalChannelInner<W>>,
}

mod sealed_raw_signal_channel {
    use super::*;

    pub struct RawSignalChannelInner<W: ?Sized + WakerSet> {
        pub(super) asserted_mask: AtomicU64,
        pub(super) active_waker: AtomicU32,
        pub(super) wakers: W,
    }

    pub trait WakerSetCanUnsize: WakerSet {
        fn unsize(
            arc: Arc<RawSignalChannelInner<Self>>,
        ) -> Arc<RawSignalChannelInner<dyn WakerSet>>;
    }

    impl<T: WakerSet> WakerSetCanUnsize for T {
        fn unsize(
            arc: Arc<RawSignalChannelInner<Self>>,
        ) -> Arc<RawSignalChannelInner<dyn WakerSet>> {
            arc
        }
    }

    impl WakerSetCanUnsize for dyn WakerSet {
        fn unsize(
            arc: Arc<RawSignalChannelInner<Self>>,
        ) -> Arc<RawSignalChannelInner<dyn WakerSet>> {
            arc
        }
    }
}

use sealed_raw_signal_channel::*;

impl<W: ?Sized + WakerSet> fmt::Debug for RawSignalChannel<W> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.snapshot().fmt(f)
    }
}

impl<W: ?Sized + WakerSet> hash::Hash for RawSignalChannel<W> {
    fn hash<H: hash::Hasher>(&self, state: &mut H) {
        Arc::as_ptr(&self.inner).hash(state);
    }
}

impl<W: ?Sized + WakerSet> Eq for RawSignalChannel<W> {}

impl<W: ?Sized + WakerSet> PartialEq for RawSignalChannel<W> {
    fn eq(&self, other: &Self) -> bool {
        Arc::ptr_eq(&self.inner, &other.inner)
    }
}

impl<W: ?Sized + WakerSet> Clone for RawSignalChannel<W> {
    fn clone(&self) -> Self {
        Self {
            inner: self.inner.clone(),
        }
    }
}

impl<W: ?Sized + WakerSet> RawSignalChannel<W> {
    pub fn new(wakers: W) -> Self
    where
        W: Sized,
    {
        Self {
            inner: Arc::new(RawSignalChannelInner {
                wakers,
                asserted_mask: AtomicU64::new(0),
                active_waker: AtomicU32::new(u32::MAX),
            }),
        }
    }

    pub fn wait<R>(&self, waker: WakerSetRef<W>, worker: impl FnOnce() -> R) -> Option<R>
    where
        W: Sized,
    {
        debug_assert_eq!(self.inner.active_waker.load(Relaxed), u32::MAX);

        self.inner.active_waker.store(waker.index(), Relaxed);

        let _undo_guard = scopeguard::guard((), |()| {
            self.inner.active_waker.store(u32::MAX, Relaxed);
        });

        fence(SeqCst);

        if self.inner.asserted_mask.load(Relaxed) != 0 {
            return None;
        }

        Some(worker())
    }

    pub fn assert(&self, mask: u64) {
        if self.inner.asserted_mask.fetch_or(mask, Relaxed) != 0 {
            return;
        }

        fence(SeqCst);

        let waker = self.inner.active_waker.load(Relaxed);

        if waker != u32::MAX {
            self.inner.wakers.wake(waker);
        }
    }

    pub fn take(&self, mask: u64) -> u64 {
        self.inner.asserted_mask.fetch_and(!mask, Relaxed) & mask
    }

    pub fn snapshot(&self) -> impl '_ + fmt::Debug + Clone {
        #[derive(Debug, Clone)]
        #[allow(unused)]
        struct RawSignalChannel {
            asserted_mask: u64,
            active_waker: FmtDebugUsingDisplay<&'static str>,
        }

        RawSignalChannel {
            asserted_mask: self.inner.asserted_mask.load(Relaxed),
            active_waker: FmtDebugUsingDisplay(
                self.inner
                    .wakers
                    .name_of(self.inner.active_waker.load(Relaxed)),
            ),
        }
    }
}

impl<W: ?Sized + WakerSet + WakerSetCanUnsize> RawSignalChannel<W> {
    pub fn bind(self, mask: u64) -> BoundSignalChannel {
        BoundSignalChannel {
            channel: self.unsize(),
            mask,
        }
    }

    pub fn bind_clone(&self, mask: u64) -> BoundSignalChannel {
        self.clone().bind(mask)
    }

    pub fn unsize(self) -> RawSignalChannel<dyn WakerSet> {
        RawSignalChannel {
            inner: W::unsize(self.inner),
        }
    }
}

// === BoundSignalChannel === //

#[derive(Debug, Clone, Hash, Eq, PartialEq)]
pub struct BoundSignalChannel {
    pub channel: RawSignalChannel<dyn WakerSet>,
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
