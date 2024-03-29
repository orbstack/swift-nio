use bytemuck::Pod;
use core::fmt;
use derive_where::derive_where;
use std::{marker::PhantomData, mem::size_of, ops::Range};

// === Mmio Range === //

pub trait IntoMmioRange: Copy {
    fn into_range(self) -> MmioRange;
}

#[derive(Copy, Clone, Hash, Eq, PartialEq)]
pub struct MmioRange {
    pub start: u64,
    pub end: u64,
}

impl fmt::Debug for MmioRange {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{:X}-{:X}[size={:X}]", self.start, self.end, self.size())
    }
}

impl MmioRange {
    pub const fn new(start: u64, end: u64) -> Self {
        Self { start, end }
    }

    pub const fn offset(self, base: u64) -> Self {
        Self {
            start: base + self.start,
            end: base + self.end,
        }
    }

    pub fn union(self, range: MmioRange) -> Self {
        let full_range = Self {
            start: self.start.min(range.start),
            end: self.end.max(range.end),
        };

        // N.B. a union range has gaps iff it manages to fit more than the two ranges inside it.
        assert!(full_range.size() <= self.size() + range.size());

        full_range
    }

    pub const fn size(self) -> u64 {
        self.end - self.start
    }
}

impl IntoMmioRange for MmioRange {
    fn into_range(self) -> MmioRange {
        self
    }
}

#[derive_where(Debug, Copy, Clone, Hash, Eq, PartialEq)]
pub struct TypedMmioRange<T> {
    pub _ty: PhantomData<fn(T) -> T>,
    pub start: u64,
    pub end: u64,
}

impl<T> TypedMmioRange<T> {
    pub const FULL: Self = Self {
        _ty: PhantomData,
        start: 0,
        end: size_of::<T>() as u64,
    };

    pub const fn reinterpret<V>(self) -> TypedMmioRange<V> {
        TypedMmioRange {
            _ty: PhantomData,
            start: self.start,
            end: self.end,
        }
    }

    pub const fn raw(self) -> MmioRange {
        MmioRange {
            start: self.start,
            end: self.end,
        }
    }
}

impl<T> IntoMmioRange for TypedMmioRange<T> {
    fn into_range(self) -> MmioRange {
        MmioRange {
            start: self.start,
            end: self.end,
        }
    }
}

#[doc(hidden)]
pub mod mmio_range_macro_internals {
    use super::TypedMmioRange;
    use std::marker::PhantomData;

    pub use std::mem::offset_of;

    pub fn create_mmio_range<In, Field>(
        _binder: impl FnOnce(&In) -> &Field,
        offset: usize,
    ) -> TypedMmioRange<Field> {
        TypedMmioRange {
            _ty: PhantomData,
            start: offset as u64,
            end: (offset + std::mem::size_of::<Field>()) as u64,
        }
    }
}

#[macro_export]
macro_rules! mmio_range {
    ($ty:path, $field:ident) => {
        $crate::mmio_util::mmio_range_macro_internals::create_mmio_range::<$ty, _>(
            |v| &v.$field,
            $crate::mmio_util::mmio_range_macro_internals::offset_of!($ty, $field),
        )
    };
}

pub use mmio_range;

// === MmioRequest === //

// MmioMode
pub trait MmioMode: Sized + 'static {
    type Slice<'b>;

    fn sub_slice<'b>(obj: &'b mut Self::Slice<'_>, range: Range<u64>) -> Self::Slice<'b>;

    fn len(obj: &Self::Slice<'_>) -> u64;
}

// MmioRequest
pub struct MmioRequest<'a, M: MmioMode> {
    offset: u64,
    data: M::Slice<'a>,
}

impl<'a, M: MmioMode> MmioRequest<'a, M> {
    pub fn new(offset: u64, data: M::Slice<'a>) -> Self {
        Self { offset, data }
    }

    pub fn len(&self) -> u64 {
        M::len(&self.data)
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }

    pub fn req_range(&self) -> MmioRange {
        MmioRange {
            start: self.offset,
            end: self.offset + self.len(),
        }
    }

    pub fn sub(&mut self, sub_range: impl IntoMmioRange) -> MmioRequest<'_, M> {
        let sub_range = sub_range.into_range();
        let req_range = self.req_range();
        let req_len = self.len();

        MmioRequest {
            // If the `sub_range` begins before the `req_range` (i.e. `sub_range.start < req_range.start`),
            // the offset of the read portion from the sub start is positive. Otherwise, it's zero
            // and we have to truncate the start of the slice appropriately.
            offset: req_range.start.saturating_sub(sub_range.start),

            data: M::sub_slice(&mut self.data, {
                // If the `sub_range` begins after the `req_range` (i.e. `sub_range.start > req_range.start`),
                // we have to truncate the start of the slice to ensure that the slice begins on the
                // first byte of interest. Otherwise, we don't need to truncate.
                //
                // If the `sub_range` start is beyond the requested range entirely, the slice should
                // be empty.
                let starts_at = (sub_range.start.saturating_sub(req_range.start)).min(req_len);

                // If the `sub_range` ends before the `req_range` ends (i.e. `sub_range.end < req_range.end`),
                // we have to truncate the data from the end by the difference in lengths to ensure
                // that the sub request doesn't view past its bounds.
                //
                // Otherwise, we don't have to truncate anything.
                let end_shrink_by = req_range.end.saturating_sub(sub_range.end);

                // Of course, if the `sub_range` ends way before the `req_range`, we may end up
                // truncating the range down to nothing.
                let ends_at = req_len.saturating_sub(end_shrink_by);

                // TODO: Prove that this range is always well-formed (should be inherited from the
                // well-formedness of the original ranges)
                starts_at..ends_at
            }),
        }
    }

    pub fn handle_sub(
        &mut self,
        sub_range: impl IntoMmioRange,
        f: impl FnOnce(MmioRequest<'_, M>),
    ) -> &mut Self {
        let less_long = &mut *self;
        let sub = less_long.sub(sub_range);
        if !sub.is_empty() {
            f(sub);
        } else {
            drop(sub);
        }

        self
    }

    pub fn handle_array<E, const N: usize>(
        &mut self,
        sub_range: TypedMmioRange<[E; N]>,
        mut f: impl FnMut(usize, MmioRequest<'_, M>),
    ) -> &mut Self {
        self.handle_sub(sub_range, |mut req| {
            let req_range = req.req_range();

            let first_elem = (req_range.start / size_of::<E>() as u64) as usize;
            let last_elem = (req_range.end.div_ceil(size_of::<E>() as u64)) as usize;

            for i in first_elem..last_elem {
                let elem_range = MmioRange {
                    start: i as u64 * size_of::<E>() as u64,
                    end: (i as u64 + 1) * (size_of::<E>() as u64),
                };

                f(i, req.sub(elem_range));
            }
        })
    }
}

// === MmioReadRequest === //

pub type MmioReadRequest<'a> = MmioRequest<'a, MmioRead>;

#[non_exhaustive]
pub struct MmioRead;

impl MmioMode for MmioRead {
    type Slice<'b> = &'b mut [u8];

    fn sub_slice<'b>(obj: &'b mut Self::Slice<'_>, range: Range<u64>) -> Self::Slice<'b> {
        &mut obj[(range.start as usize)..(range.end as usize)]
    }

    fn len(obj: &Self::Slice<'_>) -> u64 {
        obj.len() as u64
    }
}

impl MmioReadRequest<'_> {
    pub fn handle_pod<T: Pod>(
        &mut self,
        sub_range: TypedMmioRange<T>,
        f: impl FnOnce() -> T,
    ) -> &mut Self {
        self.handle_sub(sub_range, |mut req| {
            req.set_value(bytemuck::bytes_of(&f()));
        })
    }

    pub fn handle_pod_array<T: Pod, const N: usize>(
        &mut self,
        sub_range: TypedMmioRange<[T; N]>,
        mut f: impl FnMut(usize) -> T,
    ) -> &mut Self {
        self.handle_array(sub_range, |idx, mut req| {
            req.handle_pod(TypedMmioRange::<T>::FULL, || f(idx));
        })
    }

    pub fn handle_flags<T>(
        &mut self,
        sub_range: TypedMmioRange<T>,
        f: impl FnOnce() -> T,
    ) -> &mut Self
    where
        T: bitflags::Flags,
        T::Bits: Pod,
    {
        self.handle_pod(sub_range.reinterpret(), || f().bits())
    }

    pub fn set_value(&mut self, data: &[u8]) {
        let range = self.req_range();
        self.data
            .copy_from_slice(&data[(range.start as usize)..(range.end as usize)]);
    }
}

// === MmioWriteRequest === //

pub type MmioWriteRequest<'a> = MmioRequest<'a, MmioWrite>;

#[non_exhaustive]
pub struct MmioWrite;

impl MmioMode for MmioWrite {
    type Slice<'b> = &'b [u8];

    fn sub_slice<'b>(obj: &'b mut Self::Slice<'_>, range: Range<u64>) -> Self::Slice<'b> {
        &obj[(range.start as usize)..(range.end as usize)]
    }

    fn len(obj: &Self::Slice<'_>) -> u64 {
        obj.len() as u64
    }
}

impl<'a> MmioWriteRequest<'a> {
    // N.B. it is safe to read endian-dependent values here because the endianess of the guest will
    // always match the MMIO endianess.
    pub fn handle_pod<T: Pod>(
        &mut self,
        sub_range: TypedMmioRange<T>,
        f: impl FnOnce(T),
    ) -> &mut Self {
        self.handle_sub(sub_range, |req| {
            if req.offset != 0 || req.data.len() != size_of::<T>() {
                log::warn!(
                    "malformed pod write, ignoring (offset: {}, actual size: {}, expected size: {})",
                    req.offset, req.data.len(), size_of::<T>(),
                );
                return;
            }

            f(bytemuck::pod_read_unaligned(req.data))
        })
    }

    pub fn handle_pod_array<T: Pod, const N: usize>(
        &mut self,
        sub_range: TypedMmioRange<[T; N]>,
        mut f: impl FnMut(usize, T),
    ) -> &mut Self {
        self.handle_array(sub_range, |idx, mut req| {
            req.handle_pod(TypedMmioRange::<T>::FULL, |val| f(idx, val));
        })
    }

    pub fn handle_flags<T>(&mut self, sub_range: TypedMmioRange<T>, f: impl FnOnce(T)) -> &mut Self
    where
        T: bitflags::Flags,
        T::Bits: Pod,
    {
        self.handle_pod(sub_range.reinterpret(), |val| f(T::from_bits_retain(val)))
    }
}

// === BitPack === //

pub type BitPack32 = BitPack<u32>;
pub type BitPack64 = BitPack<u64>;

#[derive(Debug, Clone, Default)]
pub struct BitPack<N: num::PrimInt>(pub N);

impl<N: num::PrimInt> BitPack<N> {
    pub const BITS: usize = size_of::<N>() * 8;

    pub fn one_hot(idx: usize) -> N {
        debug_assert!(idx < Self::BITS);
        N::one() << idx
    }

    fn mask_incl_below(idx: usize) -> N {
        debug_assert!(idx < Self::BITS);
        Self::mask_excl_below(idx + 1)
    }

    fn mask_excl_below(idx: usize) -> N {
        debug_assert!(idx <= Self::BITS);

        if idx == Self::BITS {
            N::max_value()
        } else {
            Self::one_hot(idx) - N::one()
        }
    }

    pub fn set_bit(&mut self, idx: usize, value: bool) -> &mut Self {
        if value {
            self.0 = self.0 | Self::one_hot(idx);
        } else {
            self.0 = self.0 & !Self::one_hot(idx);
        }
        self
    }

    pub fn get_bit(&self, idx: usize) -> bool {
        !(self.0 & Self::one_hot(idx)).is_zero()
    }

    pub fn set_range(&mut self, start: usize, end: usize, value: N) -> &mut Self {
        assert!(end >= start);

        // Clear previous bits.
        let keep_mask =
            // Include bits above `end`...
            !Self::mask_incl_below(end) |
            // ...and below `start`
            Self::mask_excl_below(start);

        self.0 = self.0 & keep_mask;

        // Write new bits.
        let changes = value << start;
        assert!(changes <= Self::mask_incl_below(end));
        self.0 = self.0 | changes;
        self
    }

    pub fn get_range(&self, start: usize, end: usize) -> N {
        assert!(end >= start);

        // Mask out the bits above the visible range.
        let val = self.0 & Self::mask_incl_below(end);

        // Shift left to truncate the starting bits.
        val >> start
    }
}

pub fn iter_bit_ranges(idx: usize, bits: u32) -> impl Iterator<Item = (usize, usize, usize)> {
    let bpw = 32 / bits as usize;
    let mut idx = idx * bpw;
    let max_idx = idx + bpw;
    let mut bit = 0;

    std::iter::from_fn(move || {
        if idx < max_idx {
            let curr_idx = idx;
            idx += 1;

            let curr_bit = bit;
            bit += bits;

            Some((curr_idx, curr_bit as usize, bit as usize - 1))
        } else {
            None
        }
    })
}

pub fn read_bit_array(idx: usize, val: u32, bits: u32) -> impl Iterator<Item = (u32, u32)> {
    let read = BitPack(val);
    iter_bit_ranges(idx, bits)
        .map(move |(idx, start, end)| (idx as u32, read.get_range(start, end)))
}

pub fn read_set_bits(idx: usize, val: u32) -> impl Iterator<Item = u32> {
    iter_set_bits(val).map(move |offset| (idx * 4 + offset) as u32)
}

pub fn write_bit_array(idx: usize, bits: u32, mut f: impl FnMut(usize) -> u32) -> u32 {
    let mut writer = BitPack::default();

    for (idx, start, end) in iter_bit_ranges(idx, bits) {
        writer.set_range(start, end, f(idx));
    }

    writer.0
}

pub fn iter_set_bits<N: num::PrimInt>(value: N) -> impl Iterator<Item = usize> {
    let mut mask = BitPack(value);

    std::iter::from_fn(move || {
        let idx = mask.0.trailing_zeros() as usize;
        (idx != std::mem::size_of::<N>() * 8).then(|| {
            mask.set_bit(idx, false);
            idx
        })
    })
}

pub fn convert_to_pod<T: bytemuck::Pod>(val: &impl bytemuck::Pod) -> T {
    bytemuck::pod_read_unaligned(bytemuck::bytes_of(val))
}

// === CEnum === //

#[macro_export]
macro_rules! c_enum {
    ($(
        $(#[$item_attr:meta])*
        $item_vis:vis enum $item_name:ident($prim:ty) {
            $(
                $([$variant_attr:meta])*
                $variant_name:ident = $variant_val:literal
            ),*$(,)?
        }
    )*) => {$(
        $(#[$item_attr])*
        #[repr($prim)]
        $item_vis enum $item_name {$(
            $(#[$variant_attr])*
            $variant_name = $variant_val,
        )*}

        impl $item_name {
            #[allow(unused, non_snake_case)]
            pub const COUNT: ::core::primitive::usize = 0 $(+ {let $variant_name = (); 1})*;

            pub const VARIANTS: [Self; Self::COUNT] = [
                $(Self::$variant_name,)*
            ];

            pub fn try_parse(value: $prim) -> ::core::option::Option<Self> {
                match value {
                    $($variant_val => ::core::option::Option::Some(Self::$variant_name),)*
                    _ => ::core::option::Option::None,
                }
            }

            pub fn parse(value: $prim) -> Self {
                Self::try_parse(value).unwrap()
            }
        }
    )*};
}

pub use c_enum;

// === Unit Tests === //

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn sub_at_zero_offset() {
        let mut req = [0, 1, 2, 3];
        let mut req = MmioReadRequest::new(0, &mut req);

        assert!(req.sub(MmioRange::new(0, 0)).is_empty());
        assert!(req.sub(MmioRange::new(1, 1)).is_empty());
        assert!(req.sub(MmioRange::new(5, 5)).is_empty());
        assert!(req.sub(MmioRange::new(5, 10)).is_empty());

        assert_eq!(req.sub(MmioRange::new(0, 2)).data, &[0, 1]);
        assert_eq!(req.sub(MmioRange::new(1, 2)).data, &[1]);
        assert_eq!(req.sub(MmioRange::new(3, 5)).data, &[3]);
    }

    #[test]
    fn big_sub_with_offset() {
        let mut req = [0, 1, 2, 3, 4, 5, 6, 7, 8, 9];
        let mut req = MmioReadRequest::new(10, &mut req);

        assert!(req.sub(MmioRange::new(0, 0)).is_empty());
        assert!(req.sub(MmioRange::new(1, 1)).is_empty());
        assert!(req.sub(MmioRange::new(5, 5)).is_empty());
        assert!(req.sub(MmioRange::new(5, 10)).is_empty());
        assert!(req.sub(MmioRange::new(10, 10)).is_empty());

        assert_eq!(req.sub(MmioRange::new(10, 11)).data, &[0]);
        assert_eq!(req.sub(MmioRange::new(5, 11)).data, &[0]);
        assert_eq!(req.sub(MmioRange::new(5, 14)).data, &[0, 1, 2, 3]);
        assert_eq!(req.sub(MmioRange::new(20, 21)).data, &[]);
        assert_eq!(req.sub(MmioRange::new(19, 20)).data, &[9]);
        assert_eq!(req.sub(MmioRange::new(19, 21)).data, &[9]);
        assert_eq!(req.sub(MmioRange::new(18, 21)).data, &[8, 9]);
        assert_eq!(req.sub(MmioRange::new(18, 20)).data, &[8, 9]);
        assert_eq!(req.sub(MmioRange::new(11, 12)).data, &[1]);
        assert_eq!(req.sub(MmioRange::new(11, 13)).data, &[1, 2]);
        assert_eq!(
            req.sub(MmioRange::new(0, 20)).data,
            &[0, 1, 2, 3, 4, 5, 6, 7, 8, 9]
        );
    }

    #[test]
    fn bit_pack_masks() {
        assert_eq!(BitPack32::mask_incl_below(0), 0b1);
        assert_eq!(BitPack32::mask_incl_below(1), 0b11);

        assert_eq!(BitPack32::mask_excl_below(0), 0b0);
        assert_eq!(BitPack32::mask_excl_below(1), 0b1);
        assert_eq!(BitPack32::mask_excl_below(2), 0b11);
    }

    #[test]
    fn bit_pack_write_single() {
        let mut bpr = BitPack32::default();

        bpr.set_bit(0, true);
        assert_eq!(bpr.0, 1);

        bpr.set_bit(0, false);
        assert_eq!(bpr.0, 0);

        bpr.set_bit(1, true);
        assert_eq!(bpr.0, 0b10);

        bpr.set_bit(0, true);
        assert_eq!(bpr.0, 0b11);
    }

    #[test]
    fn bit_pack_write_many() {
        let mut bpr = BitPack::default();

        bpr.set_range(0, 0, 1);
        assert_eq!(bpr.0, 1);

        bpr.set_range(0, 1, 0b10);
        assert_eq!(bpr.0, 0b10);

        bpr.set_range(0, 1, 0b01);
        assert_eq!(bpr.0, 0b01);

        bpr.set_range(5, 5, 0b1);
        assert_eq!(bpr.0, 0b100001);

        bpr.set_range(4, 5, 0b01);
        assert_eq!(bpr.0, 0b010001);
    }

    #[test]
    fn bit_pack_get_range() {
        let bpr = BitPack::<u32>(0b1001);
        assert_eq!(0b1001, bpr.get_range(0, 3));
        assert_eq!(0b001, bpr.get_range(0, 2));
        assert_eq!(0b100, bpr.get_range(1, 3));
        assert_eq!(0b1, bpr.get_range(3, 3));

        let bpr = BitPack::<u32>(BitPack::one_hot(31));
        assert_eq!(0b1, bpr.get_range(31, 31));
        assert_eq!(0b10, bpr.get_range(30, 31));
    }

    #[test]
    fn iter_bit_ranges_correct() {
        assert_eq!(
            (0..32)
                .map(|i| (i as usize, i as usize, i as usize))
                .collect::<Vec<_>>(),
            iter_bit_ranges(0, 1).collect::<Vec<_>>()
        );

        assert_eq!(
            (0..32)
                .map(|i| (32 + i as usize, i as usize, i as usize))
                .collect::<Vec<_>>(),
            iter_bit_ranges(1, 1).collect::<Vec<_>>()
        );
    }

    #[test]
    fn bit_array_round_trip() {
        let bits = 2;
        let value = 573324;
        let mut arr = Vec::new();

        for (_, v) in read_bit_array(0, value, bits) {
            arr.push(v);
        }

        assert_eq!(arr.len() as u32, 32 / bits);

        assert_eq!(value, write_bit_array(0, bits, |i| arr[i]));
    }
}
