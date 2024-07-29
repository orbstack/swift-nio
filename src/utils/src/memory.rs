use vm_memory::{
    Address, ByteValued, GuestAddress, GuestMemory, GuestMemoryMmap, GuestMemoryRegion,
    MemoryRegionAddress, VolatileSlice,
};

pub trait GuestMemoryExt {
    // TODO: we actually need to add the ptr guard back for lifetime safety
    fn get_slice_fast(
        &self,
        addr: GuestAddress,
        len: usize,
    ) -> vm_memory::GuestMemoryResult<VolatileSlice>;

    unsafe fn get_obj_ptr_unaligned<T: ByteValued>(
        &self,
        addr: GuestAddress,
    ) -> vm_memory::GuestMemoryResult<*mut T> {
        let vs: VolatileSlice = self.get_slice_fast(addr, std::mem::size_of::<T>())?;
        Ok(vs.ptr_guard_mut().as_ptr() as *mut T)
    }

    fn get_obj_ptr_aligned<T: ByteValued>(
        &self,
        addr: GuestAddress,
    ) -> vm_memory::GuestMemoryResult<*mut T> {
        let ptr = unsafe { self.get_obj_ptr_unaligned(addr)? };
        // check alignment
        if (ptr as usize) % std::mem::align_of::<T>() != 0 {
            return Err(vm_memory::guest_memory::Error::HostAddressNotAvailable);
        }
        Ok(ptr)
    }

    fn read_obj_fast<T: ByteValued>(&self, addr: GuestAddress) -> vm_memory::GuestMemoryResult<T> {
        // faster than calling memcpy, and deals with unaligned ptrs (so no need to check)
        let ptr = unsafe { self.get_obj_ptr_unaligned(addr)? };
        let obj = unsafe { std::ptr::read_unaligned(ptr) };
        Ok(obj)
    }

    // these should be volatile + RefCell, but for ByteValued it doesn't matter
    // TODO: not really safe due to volatile/aliasing issues
    unsafe fn get_obj_slice<T: ByteValued>(
        &self,
        addr: GuestAddress,
        count: usize,
    ) -> vm_memory::GuestMemoryResult<&[T]> {
        let len_bytes = std::mem::size_of::<T>()
            .checked_mul(count)
            .ok_or(vm_memory::guest_memory::Error::GuestAddressOverflow)?;
        let vs = self.get_slice_fast(addr, len_bytes)?;
        // check alignment
        let ptr = vs.ptr_guard().as_ptr();
        if (ptr as usize) % std::mem::align_of::<T>() != 0 {
            return Err(vm_memory::guest_memory::Error::HostAddressNotAvailable);
        }
        let slice = unsafe { std::slice::from_raw_parts(ptr as *const _, count) };
        Ok(slice)
    }

    unsafe fn get_obj_slice_mut<T: ByteValued>(
        &self,
        addr: GuestAddress,
        count: usize,
    ) -> vm_memory::GuestMemoryResult<&mut [T]> {
        let len_bytes = std::mem::size_of::<T>()
            .checked_mul(count)
            .ok_or(vm_memory::guest_memory::Error::GuestAddressOverflow)?;
        let vs = self.get_slice_fast(addr, len_bytes)?;
        // check alignment
        let ptr = vs.ptr_guard_mut().as_ptr();
        if (ptr as usize) % std::mem::align_of::<T>() != 0 {
            return Err(vm_memory::guest_memory::Error::HostAddressNotAvailable);
        }
        let slice = unsafe { std::slice::from_raw_parts_mut(ptr as *mut _, count) };
        Ok(slice)
    }
}

impl GuestMemoryExt for GuestMemoryMmap {
    // TODO: ditch vm_memory at some point... upstream get_slice impl is slow. region scanning is also slow (should just linear search, and no Arc for each region)
    fn get_slice_fast(
        &self,
        addr: GuestAddress,
        len: usize,
    ) -> vm_memory::GuestMemoryResult<VolatileSlice> {
        let region = self
            .find_region(addr)
            .ok_or_else(|| vm_memory::guest_memory::Error::InvalidGuestAddress(addr))?;
        // safe: can't get a region if addr < start_addr
        let offset = MemoryRegionAddress(addr.raw_value() - region.start_addr().raw_value());
        // this does bounds check
        region.get_slice(offset, len)
    }
}
