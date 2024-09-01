use utils::memory::{GuestRef, GuestSlice};

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
pub fn read_regular_ref(v: &u32) -> u32 {
    *v
}

#[no_mangle]
pub fn read_guest_ref(v: GuestRef<u32>) -> u32 {
    v.read()
}
