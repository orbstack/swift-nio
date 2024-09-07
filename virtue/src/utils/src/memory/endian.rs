use std::fmt;

use bytemuck::{Pod, Zeroable};

macro_rules! define_endian {
    (
        $($name:ident, $prim:ty, $to_end:ident, $from_end:ident;)*
    ) => {$(
        #[derive(Copy, Clone, Hash, Eq, PartialEq, Default, Pod, Zeroable)]
        #[repr(transparent)]
        pub struct $name($prim);

        impl fmt::Debug for $name {
            fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                self.get().fmt(f)
            }
        }

        impl $name {
            pub const fn new(raw: $prim) -> Self {
                Self(<$prim>::$to_end(raw))
            }

            pub const fn get(self) -> $prim {
                <$prim>::$from_end(self.0)
            }

            pub const fn from_raw(raw: $prim) -> Self {
                Self(raw)
            }

            pub const fn raw(self) -> $prim {
                self.0
            }
        }

        impl From<$prim> for $name {
            fn from(v: $prim) -> Self {
                Self::new(v)
            }
        }

        impl From<$name> for $prim {
            fn from(v: $name) -> $prim {
                v.get()
            }
        }
    )*};
}

define_endian! {
    LeU16, u16, from_le, to_le;
    LeI16, i16, from_le, to_le;
    LeU32, u32, from_le, to_le;
    LeI32, i32, from_le, to_le;
    LeU64, u64, from_le, to_le;
    LeI64, i64, from_le, to_le;
    LeUSize, usize, from_le, to_le;
    LeISize, usize, from_le, to_le;

    BeU16, u16, from_be, to_be;
    BeI16, i16, from_be, to_be;
    BeU32, u32, from_be, to_be;
    BeI32, i32, from_be, to_be;
    BeU64, u64, from_be, to_be;
    BeI64, i64, from_be, to_be;
    BeUSize, usize, from_be, to_be;
    BeISize, usize, from_be, to_be;
}
