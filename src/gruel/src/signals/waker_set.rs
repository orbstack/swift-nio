use std::{
    any::{Any, TypeId},
    fmt,
    marker::PhantomData,
};

use derive_where::derive_where;

// === Traits === //

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

// === `define_waker_set!` === //

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
