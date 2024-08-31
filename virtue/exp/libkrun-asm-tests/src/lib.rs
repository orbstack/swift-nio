#[no_mangle]
pub fn increment(v: u32) -> u32 {
    v + 1
}

#[no_mangle]
pub fn multiply_by_five(v: u32) -> u32 {
    v * 5
}
