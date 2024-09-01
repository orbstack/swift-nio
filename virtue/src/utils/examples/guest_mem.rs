use anyhow::Context;
use bytemuck::{Pod, Zeroable};
use utils::{
    field,
    memory::{GuestAddress, GuestMemory},
};

fn main() {}

#[derive(Debug, Copy, Clone, Pod, Zeroable)]
#[repr(C)]
struct MyDemo {
    a: [u32; 999],
    b: i32,
}

fn do_something(mem: &GuestMemory) -> anyhow::Result<()> {
    let range = mem
        .byte_range_sized(GuestAddress::from_usize(0xDEADBEEF), 1024)
        .context("invalid range")?
        .cast_trunc::<MyDemo>();

    for entry in range {
        let values = entry.get(field!(MyDemo, a)).as_slice();

        for value in values {
            let value = value.read();
        }
    }

    Ok(())
}
