use std::sync::Arc;

use utils::memory::{GuestAddress, GuestMemory, MachVmGuestMemoryProvider};

fn main() {
    let memory = GuestMemory::new(Arc::new(
        MachVmGuestMemoryProvider::new(&[(GuestAddress(0x4000), 10)]).unwrap(),
    ));

    memory.try_write(GuestAddress(4), &[4u32]).unwrap();
}
