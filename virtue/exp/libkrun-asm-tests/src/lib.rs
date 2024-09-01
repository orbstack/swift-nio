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
