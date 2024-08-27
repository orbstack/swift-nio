use std::{
    any::{Any, TypeId},
    fmt,
    marker::PhantomData,
    sync::Arc,
};

use smallvec::SmallVec;

// === GuestAddress === //

#[derive(Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
pub struct GuestAddress(usize);

impl fmt::Debug for GuestAddress {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{:x}", self.0)
    }
}

impl GuestAddress {
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
}

// === GuestMemory === //

#[derive(Debug, Clone)]
pub struct GuestMemory {
    ranges: Arc<[GuestMemoryRangeEntry]>,
}

#[derive(Debug)]
struct GuestMemoryRangeEntry {
    range: Box<dyn GuestMemoryRange>,
    guest_base: GuestAddress,
    host_base: *mut [u8],
}

impl GuestMemory {
    pub fn new(ranges: impl IntoIterator<Item = (GuestAddress, BoxedGuestMemoryRange)>) -> Self {
        let mut ranges = ranges.into_iter().collect::<Vec<_>>();
        ranges.sort_by(|l, r| l.0.cmp(&r.0));

        let ranges = ranges
            .into_iter()
            .map(|(guest_base, range)| {
                let host_slice = range.as_slice();
                GuestMemoryRangeEntry {
                    range,
                    guest_base,
                    host_base: host_slice,
                }
            })
            .collect();

        Self { ranges }
    }

    pub fn ranges(&self) -> impl Iterator<Item = (GuestAddress, &dyn GuestMemoryRange)> {
        self.ranges.iter().map(|v| (v.guest_base, &*v.range))
    }

    pub fn range_count(&self) -> usize {
        self.ranges.len()
    }

    pub fn fetch<T: bytemuck::Pod>(
        &self,
        start: GuestAddress,
        len: usize,
    ) -> Option<GuestSlice<'_, T>> {
        todo!()
    }
}

pub struct GuestSlice<'a, T: bytemuck::Pod> {
    _ty: PhantomData<&'a [T]>,
    slices: SmallVec<[*mut [u8]; 1]>,
}

impl<T: bytemuck::Pod> GuestSlice<'_, T> {
    // TODO
}

// === GuestMemoryRange === //

pub type BoxedGuestMemoryRange = Box<dyn GuestMemoryRange>;

/// A range of memory addressable by the guest. The `meta` method can be used to provide additional
/// metadata about the region.
///
/// ## Safety
///
/// - `as_slice` must return a slice which is read/write up until this value is dropped.
///
pub unsafe trait GuestMemoryRange: 'static + fmt::Debug + Send + Sync {
    fn as_slice(&self) -> *mut [u8];

    fn meta(&self, req: Request<'_>) {
        let _ = req;
    }
}

pub struct Request<'a> {
    _ty: PhantomData<&'a ()>,
    type_id: TypeId,
    value: &'a mut dyn Any,
}

impl<'a> Request<'a> {
    pub fn new<T: 'static>(value: &'a mut Option<T>) -> Self {
        Self {
            _ty: PhantomData,
            type_id: TypeId::of::<T>(),
            value,
        }
    }

    pub fn type_id(&self) -> TypeId {
        self.type_id
    }

    pub fn downcast<T: 'static>(&self) -> Option<&Option<T>> {
        self.value.downcast_ref()
    }

    pub fn downcast_mut<T: 'static>(&mut self) -> Option<&mut Option<T>> {
        self.value.downcast_mut()
    }

    pub fn provide<T: 'static>(&mut self, f: impl FnOnce() -> T) -> &mut Self {
        if let Some(slot) = self.downcast_mut() {
            *slot = Some(f());
        }
        self
    }
}
