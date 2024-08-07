use bitflags::bitflags;
use vm_memory::ByteValued;

bitflags! {
    #[derive(Debug, Clone, Copy)]
    pub struct PvgicFlags: u32 {
        const IAR1_PENDING = 1 << 0;
        const IAR1_READ = 1 << 1;
    }

    #[derive(Debug, Clone, Copy)]
    pub struct ExitActions: u32 {
        const READ_IAR1_EL1 = 1 << 0;
    }
}

// no atomics because it's on the same CPU, but must be volatile
// kernel: pvg_cpu_state
#[derive(Debug, Clone, Copy)]
pub struct PvgicVcpuState {
    pub flags: PvgicFlags,
    // only guest reads this
    #[allow(dead_code)]
    pub pending_iar1_read: u32,
}

unsafe impl ByteValued for PvgicVcpuState {}
