use utils::memory::{GuestAddress, GuestMemory, GuestRef, GuestSlice, RangeSized};

#[no_mangle]
pub fn increment(v: u32) -> u32 {
    v + 1
}

#[no_mangle]
pub fn index_regular_inlined(v: &[u32]) -> u32 {
    v[4]
}

#[no_mangle]
pub fn index_guest_inlined(v: GuestSlice<u32>) -> u32 {
    v.get(4).read()
}

#[no_mangle]
pub fn index_regular_runtime(v: &[u32], index: usize) -> u32 {
    v[index]
}

#[no_mangle]
pub fn index_guest_runtime(v: GuestSlice<u32>, index: usize) -> u32 {
    v.get(index).read()
}

#[no_mangle]
pub fn index_regular_non_pow2_runtime(v: &[[u8; 3]], index: usize) -> [u8; 3] {
    v[index]
}

#[no_mangle]
pub fn index_guest_non_pow2_runtime(v: GuestSlice<[u8; 3]>, index: usize) -> [u8; 3] {
    v.get(index).read()
}

#[no_mangle]
pub fn sub_index_regular(v: &[u8]) {
    let v = &v[0..4];
    let _ = v[0];
    let _ = v[1];
    let _ = v[2];
    let _ = v[3];
}

#[no_mangle]
pub fn sub_index_guest(v: GuestSlice<u32>) {
    let v = v.get(0..4);
    let _ = v.get(0).read();
    let _ = v.get(1).read();
    let _ = v.get(2).read();
    let _ = v.get(3).read();
}

#[no_mangle]
pub fn read_regular_ref(v: &u32) -> u32 {
    *v
}

#[no_mangle]
pub fn read_guest_ref(v: GuestRef<u32>) -> u32 {
    v.read()
}

#[no_mangle]
pub fn index_slice_sized_constant_1(v: &[u8], start: usize) -> Option<&[u8]> {
    v.get(start..)?.get(..1010)
}

#[no_mangle]
pub fn index_slice_sized_constant_2(v: &[u8], start: usize) -> Option<&[u8]> {
    let end = start.saturating_add(1010);
    if end <= v.len() {
        Some(unsafe { std::slice::from_raw_parts(v.as_ptr().add(start), 1010) })
    } else {
        None
    }
}

#[no_mangle]
pub fn index_slice_sized_constant_3<'a>(v: &'a &'a &'a [u8], start: usize) -> Option<&'a [u8]> {
    v.get(start..)?.get(..1010)
}

#[no_mangle]
pub fn index_slice_sized_runtime_1(v: &[u8], start: usize, len: usize) -> Option<&[u8]> {
    v.get(start..)?.get(..len)
}

#[no_mangle]
pub fn index_slice_sized_runtime_2(v: &[u8], start: usize, len: usize) -> Option<&[u8]> {
    let end = start.saturating_add(len);
    if end <= v.len() {
        Some(unsafe { std::slice::from_raw_parts(v.as_ptr().add(start), len) })
    } else {
        None
    }
}

#[no_mangle]
pub fn index_guest_sized_constant_1(
    guest: &GuestMemory,
    start: usize,
) -> Option<GuestSlice<'_, u8>> {
    guest
        .range_sized(GuestAddress::from_usize(start), 1010)
        .ok()
}

#[no_mangle]
pub fn index_guest_sized_constant_2(
    v: GuestSlice<'_, u8>,
    start: usize,
) -> Option<GuestSlice<'_, u8>> {
    v.try_get(start..)?.cast_trunc::<u8>().try_get(..1010)
}

#[no_mangle]
pub fn index_guest_sized_constant_3(v: &GuestMemory, start: usize) -> Option<GuestSlice<'_, u8>> {
    v.as_slice()
        .try_get(start..)?
        .cast_trunc::<u8>()
        .try_get(..1010)
}

#[no_mangle]
pub fn index_guest_sized_constant_4(
    v: GuestSlice<'_, u8>,
    start: usize,
) -> Option<GuestSlice<'_, u8>> {
    v.try_get(RangeSized::new(start, 1010))
}

#[no_mangle]
pub fn index_guest_sized_runtime(
    guest: &GuestMemory,
    start: usize,
    len: usize,
) -> Option<GuestSlice<'_, u8>> {
    guest.range_sized(GuestAddress::from_usize(start), len).ok()
}
