#![allow(clippy::missing_safety_doc)]

use std::{
    any::Any,
    fmt,
    marker::PhantomData,
    mem,
    ops::{
        Add, AddAssign, Bound, Range, RangeFrom, RangeFull, RangeInclusive, RangeTo,
        RangeToInclusive, Sub, SubAssign,
    },
    ptr::NonNull,
    sync::{
        atomic::{compiler_fence, Ordering::*},
        Arc,
    },
};

use derive_where::derive_where;

// === GuestAddress === //

#[derive(Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct GuestAddress(pub usize);

impl fmt::Debug for GuestAddress {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "0x{:X}", self.0)
    }
}

impl GuestAddress {
    pub const MIN: Self = Self::from_u32(0);

    pub const fn from_u32(v: u32) -> Self {
        Self(v as usize)
    }

    pub const fn from_u64(v: u64) -> Self {
        Self(v as usize)
    }

    pub const fn from_usize(v: usize) -> Self {
        Self(v)
    }

    pub const fn u32(self) -> Option<u32> {
        if self.0 <= u32::MAX as usize {
            Some(self.0 as u32)
        } else {
            None
        }
    }

    pub const fn u32_trunc(self) -> u32 {
        self.0 as u32
    }

    pub const fn u64(self) -> u64 {
        self.0 as u64
    }

    pub const fn usize(self) -> usize {
        self.0
    }

    pub const fn add(self, rhs: usize) -> Self {
        Self(self.0 + rhs)
    }

    pub const fn sub(self, rhs: usize) -> Self {
        Self(self.0 - rhs)
    }

    pub const fn saturating_add(self, rhs: usize) -> Self {
        Self(self.0.saturating_add(rhs))
    }

    pub const fn saturating_sub(self, rhs: usize) -> Self {
        Self(self.0.saturating_sub(rhs))
    }

    pub const fn saturating_add_signed(self, rhs: isize) -> Self {
        Self(self.0.saturating_add_signed(rhs))
    }

    pub const fn wrapping_add(self, rhs: usize) -> Self {
        Self(self.0.wrapping_add(rhs))
    }

    pub const fn wrapping_add_signed(self, rhs: isize) -> Self {
        Self(self.0.wrapping_add_signed(rhs))
    }

    pub const fn wrapping_sub(self, rhs: usize) -> Self {
        Self(self.0.wrapping_sub(rhs))
    }

    pub const fn checked_add(self, rhs: usize) -> Option<Self> {
        match self.0.checked_add(rhs) {
            Some(v) => Some(Self(v)),
            None => None,
        }
    }

    pub const fn checked_add_signed(self, rhs: isize) -> Option<Self> {
        match self.0.checked_add_signed(rhs) {
            Some(v) => Some(Self(v)),
            None => None,
        }
    }

    pub const fn checked_sub(self, rhs: usize) -> Option<Self> {
        match self.0.checked_sub(rhs) {
            Some(v) => Some(Self(v)),
            None => None,
        }
    }

    pub fn map_usize(self, f: impl FnOnce(usize) -> usize) -> Self {
        Self(f(self.0))
    }
}

impl Add<usize> for GuestAddress {
    type Output = Self;

    fn add(self, rhs: usize) -> Self::Output {
        self.add(rhs)
    }
}

impl AddAssign<usize> for GuestAddress {
    fn add_assign(&mut self, rhs: usize) {
        *self = *self + rhs;
    }
}

impl Sub<usize> for GuestAddress {
    type Output = Self;

    fn sub(self, rhs: usize) -> Self::Output {
        self.sub(rhs)
    }
}

impl SubAssign<usize> for GuestAddress {
    fn sub_assign(&mut self, rhs: usize) {
        *self = *self - rhs;
    }
}

// === GuestMemory === //

#[derive(Clone)]
pub struct GuestMemory(Arc<GuestMemoryInner>);

impl fmt::Debug for GuestMemory {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("GuestMemory").field(&self.as_ptr()).finish()
    }
}

impl GuestMemory {
    /// Creates a new `GuestMemory` instance backed by `reserved`. The `unreserve` closure will be
    /// called once all remaining `GuestMemory` clones are released.
    ///
    /// ## Safety
    ///
    /// For this API to be sound...
    ///
    /// - `reserved` must be valid for reads and writes for the duration of this object's existence.
    /// - `reserved` must not be larger than `isize::MAX` in size.
    ///
    pub unsafe fn new(reserved: NonNull<[u8]>, unreserve: impl 'static + Send + FnOnce()) -> Self {
        let guard = scopeguard::guard((), move |()| unreserve());
        Self(Arc::new(GuestMemoryInner {
            reserved,
            _unreserve_guard: Box::new(guard),
        }))
    }

    pub fn as_ptr(&self) -> NonNull<[u8]> {
        self.0.reserved
    }

    pub fn len(&self) -> usize {
        self.0.reserved.len()
    }

    pub fn as_slice(&self) -> GuestSlice<'_, u8> {
        unsafe { GuestSlice::new_unchecked(self.as_ptr()) }
    }

    pub fn byte_range(&self, range: Range<GuestAddress>) -> Option<GuestSlice<'_, u8>> {
        self.as_slice()
            .try_get(range.start.usize()..range.end.usize())
    }

    pub fn byte_range_sized(&self, base: GuestAddress, len: usize) -> Option<GuestSlice<'_, u8>> {
        self.as_slice().try_get(RangeSized::new(base.usize(), len))
    }

    pub fn range_sized<T: bytemuck::Pod>(
        &self,
        base: GuestAddress,
        len: usize,
    ) -> Option<GuestSlice<'_, T>> {
        self.as_slice()
            .cast_trunc()
            .try_get(RangeSized::new(base.usize(), len))
    }

    pub fn reference<T: bytemuck::Pod>(&self, addr: GuestAddress) -> Option<GuestRef<'_, T>> {
        self.range_sized(addr, 1).map(|v| v.get(0))
    }

    pub fn owns_ref<T: bytemuck::Pod>(&self, ptr: GuestRef<'_, T>) -> bool {
        if mem::size_of::<T>() == 0 {
            return true;
        }

        let reserved_base = self.as_ptr().as_ptr().cast::<u8>() as usize;
        let ptr_base = ptr.as_ptr().as_ptr() as usize;

        (reserved_base..(reserved_base + self.len())).contains(&ptr_base)
    }

    pub fn owns_slice<T: bytemuck::Pod>(&self, slice: GuestSlice<'_, T>) -> bool {
        if mem::size_of::<T>() == 0 {
            return true;
        }

        let reserved_base = self.as_ptr().as_ptr().cast::<u8>() as usize;
        let ptr_base = slice.as_ptr().as_ptr().cast::<u8>() as usize;
        (reserved_base..(reserved_base + self.len())).contains(&ptr_base)
    }

    pub fn address_of<T: bytemuck::Pod>(&self, ptr: GuestRef<'_, T>) -> GuestAddress {
        assert!(
            self.owns_ref(ptr),
            "reference must be owned by this `GuestMemory`"
        );

        self.address_of_in_memory(ptr)
    }

    fn address_of_in_memory<T: bytemuck::Pod>(&self, ptr: GuestRef<'_, T>) -> GuestAddress {
        struct NotZst<T>(T);

        impl<T> NotZst<T> {
            const IS_NOT_ZST: bool = {
                if mem::size_of::<T>() == 0 {
                    panic!("cannot take the guest address of a ZST");
                }

                true
            };
        }

        assert!(NotZst::<T>::IS_NOT_ZST);

        let reserved_base = self.as_ptr().as_ptr().cast::<u8>() as usize;
        let ptr_base = ptr.as_ptr().as_ptr() as usize;

        GuestAddress(ptr_base - reserved_base)
    }
}

struct GuestMemoryInner {
    reserved: NonNull<[u8]>,
    _unreserve_guard: Box<dyn Any + Send + Sync>,
}

unsafe impl Send for GuestMemoryInner {}
unsafe impl Sync for GuestMemoryInner {}

// === GuestSlice === //

#[derive_where(Copy, Clone)]
pub struct GuestSlice<'a, T: bytemuck::Pod> {
    _ty: PhantomData<&'a GuestMemory>,
    ptr: NonNull<[T]>,
}

impl<T: bytemuck::Pod> fmt::Debug for GuestSlice<'_, T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.ptr.fmt(f)
    }
}

impl<'a, T: bytemuck::Pod> GuestSlice<'a, T> {
    pub unsafe fn new_unchecked(ptr: NonNull<[T]>) -> Self {
        Self {
            _ty: PhantomData,
            ptr,
        }
    }

    pub fn as_ptr(self) -> NonNull<[T]> {
        self.ptr
    }

    pub unsafe fn erase_lifetime(self) -> GuestSlice<'static, T> {
        GuestSlice::new_unchecked(self.as_ptr())
    }

    #[track_caller]
    pub fn try_get<I: GuestSliceIndex<Self>>(self, idx: I) -> Option<I::Output> {
        idx.try_get(&self)
    }

    #[track_caller]
    pub fn get<I: GuestSliceIndex<Self>>(self, idx: I) -> I::Output {
        idx.get(&self)
    }

    #[track_caller]
    pub unsafe fn get_unchecked<I: GuestSliceIndex<Self>>(self, idx: I) -> I::Output {
        idx.get_unchecked(&self)
    }

    pub fn cast_trunc<V: bytemuck::Pod>(self) -> GuestSlice<'a, V> {
        let len = self.len() * mem::size_of::<T>();
        let len = len / mem::size_of::<V>();

        unsafe {
            GuestSlice::new_unchecked(NonNull::slice_from_raw_parts(
                self.as_ptr().cast::<V>(),
                len,
            ))
        }
    }

    pub fn cast_exact<V: bytemuck::Pod>(self) -> Option<GuestSlice<'a, V>> {
        let len = self.len() * mem::size_of::<T>();
        if len % mem::size_of::<V>() != 0 {
            return None;
        }

        let len = len / mem::size_of::<V>();

        Some(unsafe {
            GuestSlice::new_unchecked(NonNull::slice_from_raw_parts(
                self.as_ptr().cast::<V>(),
                len,
            ))
        })
    }

    pub fn write(self, i: usize, value: T) {
        self.get(i).write(value);
    }

    pub fn read(self, i: usize) -> T {
        self.get(i).read()
    }

    pub fn len(self) -> usize {
        self.ptr.len()
    }

    pub fn is_empty(self) -> bool {
        self.len() == 0
    }

    pub fn advance_one(&mut self) -> Option<GuestRef<'a, T>> {
        let first = self.try_get(0);
        if first.is_some() {
            *self = self.get(1..);
        }

        first
    }

    pub fn advance(&mut self, count: usize) -> Option<GuestSlice<'a, T>> {
        let first = self.try_get(..count);
        if first.is_some() {
            *self = self.get(count..);
        }

        first
    }
}

impl<'a, T: bytemuck::Pod> IntoIterator for GuestSlice<'a, T> {
    type Item = GuestRef<'a, T>;
    type IntoIter = GuestSliceIter<'a, T>;

    fn into_iter(self) -> Self::IntoIter {
        GuestSliceIter {
            cursor: unsafe { GuestRef::new_unchecked(self.ptr.cast::<T>()) },
            end: unsafe { GuestRef::new_unchecked(self.ptr.cast::<T>().add(self.len())) },
            len: self.len(),
        }
    }
}

#[derive_where(Debug, Clone)]
pub struct GuestSliceIter<'a, T: bytemuck::Pod> {
    cursor: GuestRef<'a, T>,
    end: GuestRef<'a, T>,
    len: usize,
}

impl<'a, T: bytemuck::Pod> ExactSizeIterator for GuestSliceIter<'a, T> {}

impl<'a, T: bytemuck::Pod> Iterator for GuestSliceIter<'a, T> {
    type Item = GuestRef<'a, T>;

    fn next(&mut self) -> Option<Self::Item> {
        (self.cursor != self.end).then(|| {
            let curr = self.cursor;
            self.cursor = unsafe { GuestRef::new_unchecked(self.cursor.as_ptr().add(1)) };

            curr
        })
    }

    fn size_hint(&self) -> (usize, Option<usize>) {
        (self.len, Some(self.len))
    }
}

// === GuestSliceIndex === //

#[cold]
#[inline(never)]
#[track_caller]
fn fail_overflow_range_bound_start() -> ! {
    panic!("range start bound at usize::MAX")
}

#[cold]
#[inline(never)]
#[track_caller]
fn fail_overflow_range_bound_end() -> ! {
    panic!("range end bound at usize::MAX")
}

#[cold]
#[inline(never)]
#[track_caller]
fn fail_index_out_of_bounds(i: usize, len: usize) -> ! {
    panic!("index out of bounds ({i} >= {len})")
}

#[cold]
#[inline(never)]
#[track_caller]
fn fail_bad_range(start: usize, end: usize, len: usize) -> ! {
    if end > start {
        panic!("range bounds are reversed: {start} > {end}")
    } else {
        panic!("range indexes out of bounds: {end} > {len}")
    }
}

#[cold]
#[inline(never)]
#[track_caller]
fn fail_bad_range_inclusive(start: usize, end: usize, len: usize) -> ! {
    if end == usize::MAX {
        fail_overflow_range_bound_end()
    } else {
        fail_bad_range(start, end + 1, len)
    }
}

fn bounds_into_range_unchecked(
    (start, end): (Bound<usize>, Bound<usize>),
    len: usize,
) -> Range<usize> {
    use std::ops::Bound::*;

    let start = match start {
        Included(start) => start,
        Excluded(start) => start + 1,
        Unbounded => 0,
    };

    let end = match end {
        Included(end) => end + 1,
        Excluded(end) => end,
        Unbounded => len,
    };

    start..end
}

pub fn bounds_into_range_checked(
    (start, end): (Bound<usize>, Bound<usize>),
    len: usize,
) -> Option<Range<usize>> {
    use std::ops::Bound::*;

    let start = match start {
        Included(start) => start,
        Excluded(start) => start.checked_add(1)?,
        Unbounded => 0,
    };

    let end = match end {
        Included(end) => end.checked_add(1)?,
        Excluded(end) => end,
        Unbounded => len,
    };

    Some(start..end)
}

pub fn bounds_into_range_packing(
    (start, end): (Bound<usize>, Bound<usize>),
    len: usize,
) -> Range<usize> {
    use std::ops::Bound::*;

    let start = match start {
        Included(start) => start,
        Excluded(start) => start
            .checked_add(1)
            .unwrap_or_else(|| fail_overflow_range_bound_start()),
        Unbounded => 0,
    };

    let end = match end {
        Included(end) => end
            .checked_add(1)
            .unwrap_or_else(|| fail_overflow_range_bound_end()),
        Excluded(end) => end,
        Unbounded => len,
    };

    start..end
}

fn inclusive_to_exclusive(range: RangeInclusive<usize>) -> Range<usize> {
    // Handles exhausted ranges differently than `RangeInclusive::into_inner` since we can't easily
    // determine the exhaustion state of ranges.
    *range.start()..(*range.end() + 1)
}

pub trait GuestSliceIndex<T> {
    type Output;

    unsafe fn get_unchecked(self, target: &T) -> Self::Output;

    fn try_get(self, target: &T) -> Option<Self::Output>;

    fn get(self, target: &T) -> Self::Output;
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for usize {
    type Output = GuestRef<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        GuestRef::new_unchecked(target.ptr.cast::<T>().add(self))
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (self < target.len()).then(|| unsafe { self.get_unchecked(target) })
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        match self.try_get(target) {
            Some(out) => out,
            None => fail_index_out_of_bounds(self, target.len()),
        }
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for Range<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        let new_len = self.end.unchecked_sub(self.start);
        GuestSlice::new_unchecked(NonNull::slice_from_raw_parts(
            target.as_ptr().cast::<T>().add(self.start),
            new_len,
        ))
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (self.start <= self.end && self.end <= target.len())
            .then(|| unsafe { self.get_unchecked(target) })
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        match self.clone().try_get(target) {
            Some(out) => out,
            None => fail_bad_range(self.start, self.end, target.len()),
        }
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeInclusive<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        inclusive_to_exclusive(self).get_unchecked(target)
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (*self.end() != usize::MAX)
            .then(|| inclusive_to_exclusive(self))
            .and_then(|v| v.try_get(target))
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        match self.clone().try_get(target) {
            Some(out) => out,
            None => fail_bad_range_inclusive(*self.start(), *self.end(), target.len()),
        }
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeFull {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        *target
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        Some(*target)
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        *target
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeFrom<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        (self.start..target.len()).get_unchecked(target)
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (self.start..target.len()).try_get(target)
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        (self.start..target.len()).get(target)
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeTo<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        (0..self.end).get_unchecked(target)
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (0..self.end).try_get(target)
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        (0..self.end).get(target)
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeToInclusive<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        (0..=self.end).get_unchecked(target)
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (0..=self.end).try_get(target)
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        (0..=self.end).get(target)
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for (Bound<usize>, Bound<usize>) {
    type Output = GuestSlice<'a, T>;

    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        bounds_into_range_unchecked(self, target.len()).get_unchecked(target)
    }

    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        bounds_into_range_checked(self, target.len()).and_then(|v| v.try_get(target))
    }

    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        bounds_into_range_packing(self, target.len()).get(target)
    }
}

#[derive(Debug, Copy, Clone)]
pub struct RangeSized {
    pub start: usize,
    pub len: usize,
}

impl RangeSized {
    pub const fn new(start: usize, len: usize) -> Self {
        Self { start, len }
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeSized {
    type Output = GuestSlice<'a, T>;

    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        target.get_unchecked(self.start..).get_unchecked(..self.len)
    }

    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        target
            .try_get(self.start..)
            .and_then(|v| v.try_get(..self.len))
    }

    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        target.get(self.start..).get(..self.len)
    }
}

// === GuestRef === //

#[derive_where(Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct GuestRef<'a, T: bytemuck::Pod> {
    _ty: PhantomData<&'a GuestMemory>,
    ptr: NonNull<T>,
}

impl<'a, T: bytemuck::Pod> fmt::Debug for GuestRef<'a, T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.ptr.fmt(f)
    }
}

impl<'a, T: bytemuck::Pod> GuestRef<'a, T> {
    pub unsafe fn new_unchecked(ptr: NonNull<T>) -> Self {
        Self {
            _ty: PhantomData,
            ptr,
        }
    }

    pub fn as_ptr(self) -> NonNull<T> {
        self.ptr
    }

    pub unsafe fn erase_lifetime(self) -> GuestRef<'static, T> {
        GuestRef::new_unchecked(self.as_ptr())
    }

    pub fn as_mono_slice(self) -> GuestSlice<'a, T> {
        unsafe { GuestSlice::new_unchecked(NonNull::slice_from_raw_parts(self.as_ptr(), 1)) }
    }

    pub fn write(self, value: T) {
        compiler_fence(SeqCst);
        unsafe { self.ptr.write_unaligned(value) };
        compiler_fence(SeqCst);
    }

    pub fn read(self) -> T {
        compiler_fence(SeqCst);
        let value = unsafe { self.ptr.read_unaligned() };
        compiler_fence(SeqCst);
        value
    }

    pub fn get<V: bytemuck::Pod>(self, field: Field<T, V>) -> GuestRef<'a, V> {
        unsafe { GuestRef::new_unchecked(self.ptr.cast::<u8>().add(field.offset).cast::<V>()) }
    }
}

impl<'a, T: bytemuck::Pod, const N: usize> GuestRef<'a, [T; N]>
where
    [T; N]: bytemuck::Pod,
{
    pub fn as_slice(self) -> GuestSlice<'a, T> {
        unsafe { GuestSlice::new_unchecked(NonNull::slice_from_raw_parts(self.ptr.cast::<T>(), N)) }
    }
}

#[derive_where(Copy, Clone, Hash, Eq, PartialEq)]
pub struct Field<T, V> {
    _ty: PhantomData<fn() -> (T, V)>,
    offset: usize,
}

impl<T, V> Field<T, V> {
    pub const unsafe fn new(offset: usize) -> Self {
        Self {
            _ty: PhantomData,
            offset,
        }
    }

    pub const fn offset(&self) -> usize {
        self.offset
    }
}

#[doc(hidden)]
pub mod field_macro_internals {
    use std::marker::PhantomData;

    use super::Field;

    pub use std::mem::offset_of;

    pub const fn create_field<T, V>(_binder: fn(&T) -> &V, offset: usize) -> Field<T, V> {
        Field {
            _ty: PhantomData,
            offset,
        }
    }
}

#[macro_export]
macro_rules! field {
    ($Ty:ty, $($fields:tt)+) => {
        $crate::memory::field_macro_internals::create_field::<$Ty, _>(
            |v| (&v $(.$fields)*),
            $crate::memory::field_macro_internals::offset_of!($Ty, $($fields)*),
        )
    };
}

pub use field;
