use std::marker::PhantomData;

use derive_where::derive_where;

#[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
pub struct RawBitFlagRange {
    pub base: u64,
    pub count: usize,
}

impl RawBitFlagRange {
    pub const fn new_seq<const COUNT: usize>(elems: [u64; COUNT]) -> Self {
        let base = elems[0];

        let mut i = 0;

        while i < elems.len() {
            if elems[i] != 1 << elems[i].trailing_zeros() {
                panic!("each element of a `BitFlagRange` may only contain one set bit");
            }

            if elems[i] != base << i {
                panic!(
                    "each subsequent element of a `BitFlagRange` must have its one-hot bit set \
                     one higher than the previous bit",
                );
            }

            i += 1;
        }

        Self { base, count: COUNT }
    }

    pub const fn get(self, idx: usize) -> u64 {
        debug_assert!(idx < self.count);

        self.base << idx
    }
}

#[derive_where(Debug, Copy, Clone, Hash, Eq, PartialEq)]
pub struct BitFlagRange<S: bitflags::Flags<Bits = u64>> {
    pub _ty: PhantomData<fn(S) -> S>,
    pub raw: RawBitFlagRange,
}

impl<S: bitflags::Flags<Bits = u64>> BitFlagRange<S> {
    pub const fn wrap_raw(raw: RawBitFlagRange) -> Self {
        Self {
            _ty: PhantomData,
            raw,
        }
    }
}

#[doc(hidden)]
pub mod make_bit_flag_range_internals {
    use super::{BitFlagRange, RawBitFlagRange};

    pub const fn make_range_count<S, const COUNT: usize>(
        _orig: &S,
        base: u64,
        count: usize,
    ) -> BitFlagRange<S>
    where
        S: bitflags::Flags<Bits = u64>,
    {
        BitFlagRange::wrap_raw(RawBitFlagRange { base, count })
    }

    pub const fn make_range_seq<S, const COUNT: usize>(
        _orig: &[S; COUNT],
        raw: [u64; COUNT],
    ) -> BitFlagRange<S>
    where
        S: bitflags::Flags<Bits = u64>,
    {
        BitFlagRange::wrap_raw(RawBitFlagRange::new_seq(raw))
    }
}

#[macro_export]
macro_rules! make_bit_flag_range {
    ($base:expr => $count:expr) => {
        $crate::bitflags::make_bit_flag_range_internals::make_range_count(
            &$base,
            $base.bits(),
            $count,
        )
    };
    ([$($expr:expr),+$(,)?]) => {{
        $crate::bitflags::make_bit_flag_range_internals::make_range_seq(
            &[$($expr,)*],
            [$($expr.bits(),)*],
        )
    }};
}
