use std::marker::PhantomData;

use derive_where::derive_where;

#[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
pub struct RawBitFlagRange {
    pub base: u64,
    pub count: usize,
}

impl RawBitFlagRange {
    pub const fn new_in_mask(mask: u64) -> Self {
        let count = mask.count_ones() as usize;
        let base = mask.trailing_zeros() as u64;

        // TODO: This was copied from `gicv3`—maybe we should have a unified helper for this too?
        const fn mask_excl_below(idx: usize) -> u64 {
            if idx >= 64 {
                u64::MAX
            } else {
                (1 << idx) - 1
            }
        }

        const fn mask_incl_below(idx: usize) -> u64 {
            mask_excl_below(idx + 1)
        }

        let expected = mask_incl_below(base as usize + count) & !mask_excl_below(base as usize);

        if mask != expected {
            panic!("Mask must be a contiguous range");
        }

        Self { base, count }
    }

    pub const fn new_seq<const COUNT: usize>(elems: [u64; COUNT]) -> Self {
        let base_mask = elems[0];

        let mut i = 0;

        while i < elems.len() {
            if elems[i] != 1 << elems[i].trailing_zeros() {
                panic!("each element of a `BitFlagRange` may only contain one set bit");
            }

            if elems[i] != base_mask << i {
                panic!(
                    "each subsequent element of a `BitFlagRange` must have its one-hot bit set \
                     one higher than the previous bit",
                );
            }

            i += 1;
        }

        let base = base_mask.trailing_zeros() as u64;

        Self { base, count: COUNT }
    }

    pub const fn opt_get(self, idx: usize) -> Option<u64> {
        if idx < self.count {
            Some(1 << (self.base + idx as u64))
        } else {
            None
        }
    }

    pub const fn get(self, idx: usize) -> u64 {
        if let Some(idx) = self.opt_get(idx) {
            idx
        } else {
            panic!("index out of bitflag range");
        }
    }

    pub fn iter(self) -> impl Iterator<Item = u64> {
        (0..self.count).map(move |i| self.base << i)
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

    pub fn base(self) -> S {
        S::from_bits_retain(self.raw.base)
    }

    pub fn count(self) -> usize {
        self.raw.count
    }

    pub fn opt_get(self, idx: usize) -> Option<S> {
        self.raw.opt_get(idx).map(S::from_bits_retain)
    }

    pub fn get(self, idx: usize) -> S {
        self.opt_get(idx).unwrap()
    }

    pub fn iter(self) -> impl Iterator<Item = S> {
        self.raw.iter().map(S::from_bits_retain)
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

    pub const fn make_range_mask<S>(_orig: &S, mask: u64) -> BitFlagRange<S>
    where
        S: bitflags::Flags<Bits = u64>,
    {
        BitFlagRange::wrap_raw(RawBitFlagRange::new_in_mask(mask))
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
    (mask $mask:expr) => {
        $crate::bitflags::make_bit_flag_range_internals::make_range_mask(
            &$mask,
            $mask.bits(),
        )
    };
    ([$($expr:expr),+$(,)?]) => {{
        $crate::bitflags::make_bit_flag_range_internals::make_range_seq(
            &[$($expr,)*],
            [$($expr.bits(),)*],
        )
    }};
}
