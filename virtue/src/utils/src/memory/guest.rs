#![allow(clippy::missing_safety_doc)]

use std::{
    any::Any,
    fmt,
    marker::PhantomData,
    mem,
    ops::{
        Add, AddAssign, Range, RangeFrom, RangeFull, RangeInclusive, RangeTo, RangeToInclusive,
        Sub, SubAssign,
    },
    ptr::NonNull,
    sync::Arc,
};

use derive_where::derive_where;

// === GuestAddress === //

#[derive(Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct GuestAddress(usize);

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

    pub fn map_usize(self, f: impl FnOnce(usize) -> usize) -> Self {
        Self(f(self.0))
    }

    // TODO: Wrapping and (explicitly) checked variants
}

impl Add<usize> for GuestAddress {
    type Output = Self;

    fn add(self, rhs: usize) -> Self::Output {
        self.map_usize(|v| v + rhs)
    }
}

impl Add<isize> for GuestAddress {
    type Output = Self;

    fn add(self, rhs: isize) -> Self::Output {
        // TODO: Make checked
        self.map_usize(|v| v.wrapping_add_signed(rhs))
    }
}

impl AddAssign<usize> for GuestAddress {
    fn add_assign(&mut self, rhs: usize) {
        *self = *self + rhs;
    }
}

impl AddAssign<isize> for GuestAddress {
    fn add_assign(&mut self, rhs: isize) {
        *self = *self + rhs;
    }
}

impl Sub<usize> for GuestAddress {
    type Output = Self;

    fn sub(self, rhs: usize) -> Self::Output {
        self.map_usize(|v| v - rhs)
    }
}

impl Sub<isize> for GuestAddress {
    type Output = Self;

    fn sub(self, rhs: isize) -> Self::Output {
        // TODO: Make checked
        self.map_usize(|v| v.wrapping_add_signed(-rhs))
    }
}

impl SubAssign<usize> for GuestAddress {
    fn sub_assign(&mut self, rhs: usize) {
        *self = *self - rhs;
    }
}

impl SubAssign<isize> for GuestAddress {
    fn sub_assign(&mut self, rhs: isize) {
        *self = *self - rhs;
    }
}

// === Index utils === //

#[cold]
#[inline(never)]
#[track_caller]
fn index_out_of_bounds(i: usize, len: usize) -> ! {
    panic!("index out of bounds ({i} >= {len})")
}

// === GuestMemory === //

#[derive(Clone)]
pub struct GuestMemory(Arc<GuestMemoryInner>);

impl fmt::Debug for GuestMemory {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("GuestMemory").field(&self.as_raw()).finish()
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

    pub fn as_raw(&self) -> NonNull<[u8]> {
        self.0.reserved
    }

    pub fn as_slice(&self) -> GuestSlice<'_, u8> {
        unsafe { GuestSlice::new_unchecked(self.as_raw().as_ptr()) }
    }

    pub fn byte_range(&self, range: Range<GuestAddress>) -> Option<GuestSlice<'_, u8>> {
        self.as_slice()
            .try_get(range.start.usize()..range.end.usize())
    }

    pub fn byte_range_sized(&self, base: GuestAddress, len: usize) -> Option<GuestSlice<'_, u8>> {
        self.as_slice().try_get(RangeSized::new(base.usize(), len))
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
    _ty: PhantomData<&'a [T]>,
    ptr: NonNull<[T]>,
}

impl<T: bytemuck::Pod> fmt::Debug for GuestSlice<'_, T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.ptr.fmt(f)
    }
}

impl<'a, T: bytemuck::Pod> GuestSlice<'a, T> {
    pub unsafe fn new_unchecked(ptr: *mut [T]) -> Self {
        Self {
            _ty: PhantomData,
            ptr: NonNull::new_unchecked(ptr),
        }
    }

    pub fn as_ptr(self) -> NonNull<[T]> {
        self.ptr
    }

    pub fn as_guest_addr(self) -> Range<GuestAddress> {
        todo!()
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

        GuestSlice {
            _ty: PhantomData,
            ptr: NonNull::slice_from_raw_parts(self.as_ptr().cast::<V>(), len),
        }
    }

    pub fn cast_exact<V: bytemuck::Pod>(self) -> Option<GuestSlice<'a, V>> {
        let len = self.len() * mem::size_of::<T>();
        if len % mem::size_of::<V>() != 0 {
            return None;
        }

        let len = len / mem::size_of::<V>();

        Some(GuestSlice {
            _ty: PhantomData,
            ptr: NonNull::slice_from_raw_parts(self.as_ptr().cast::<V>(), len),
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
        todo!()
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
            self.cursor = unsafe { GuestRef::new_unchecked(self.cursor.as_ptr().as_ptr().add(1)) };

            curr
        })
    }

    fn size_hint(&self) -> (usize, Option<usize>) {
        (self.len, Some(self.len))
    }
}

// === GuestSliceIndex === //

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
        GuestRef::new_unchecked(target.ptr.as_ptr().cast::<T>().add(self))
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        (self < target.len()).then(|| unsafe { self.get_unchecked(target) })
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        match self.try_get(target) {
            Some(out) => out,
            None => index_out_of_bounds(self, target.len()),
        }
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for Range<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeInclusive<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeFull {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeFrom<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeTo<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }
}

impl<'a, T: bytemuck::Pod> GuestSliceIndex<GuestSlice<'a, T>> for RangeToInclusive<usize> {
    type Output = GuestSlice<'a, T>;

    #[track_caller]
    unsafe fn get_unchecked(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }

    #[track_caller]
    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    #[track_caller]
    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
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
        todo!()
    }

    fn try_get(self, target: &GuestSlice<'a, T>) -> Option<Self::Output> {
        todo!()
    }

    fn get(self, target: &GuestSlice<'a, T>) -> Self::Output {
        todo!()
    }
}

// === GuestRef === //

#[derive_where(Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct GuestRef<'a, T: bytemuck::Pod> {
    _ty: PhantomData<&'a T>,
    ptr: NonNull<T>,
}

impl<'a, T: bytemuck::Pod> fmt::Debug for GuestRef<'a, T> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        self.ptr.fmt(f)
    }
}

impl<'a, T: bytemuck::Pod> GuestRef<'a, T> {
    pub unsafe fn new_unchecked(ptr: *mut T) -> Self {
        Self {
            _ty: PhantomData,
            ptr: NonNull::new_unchecked(ptr),
        }
    }

    pub fn as_ptr(self) -> NonNull<T> {
        self.ptr
    }

    pub fn as_guest_addr(self) -> GuestAddress {
        todo!()
    }

    pub fn as_mono_slice(self) -> GuestSlice<'a, T> {
        unsafe {
            GuestSlice::new_unchecked(std::slice::from_raw_parts_mut(self.as_ptr().as_ptr(), 1))
        }
    }

    pub fn write(self, value: T) {
        unsafe { self.ptr.write_volatile(value) };
    }

    pub fn read(self) -> T {
        unsafe { self.ptr.read_volatile() }
    }

    pub fn get<V: bytemuck::Pod>(self, field: Field<T, V>) -> GuestRef<'a, V> {
        unsafe {
            GuestRef::new_unchecked(self.ptr.as_ptr().cast::<u8>().add(field.offset).cast::<V>())
        }
    }
}

impl<'a, T: bytemuck::Pod, const N: usize> GuestRef<'a, [T; N]>
where
    [T; N]: bytemuck::Pod,
{
    pub fn as_slice(self) -> GuestSlice<'a, T> {
        unsafe {
            GuestSlice::new_unchecked(std::slice::from_raw_parts_mut(
                self.ptr.as_ptr().cast::<T>(),
                N,
            ))
        }
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
