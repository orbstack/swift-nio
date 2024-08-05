mod frame_pointer;
#[cfg(feature = "profiler-framehop")]
mod framehop;

pub use frame_pointer::FramePointerUnwinder;
#[cfg(feature = "profiler-framehop")]
pub use framehop::FramehopUnwinder;

pub const STACK_DEPTH_LIMIT: usize = 128;

#[derive(thiserror::Error, Debug)]
pub enum UnwindError {}

pub type Result<T> = std::result::Result<T, UnwindError>;

#[derive(Debug, Copy, Clone)]
pub struct UnwindRegs {
    pub pc: u64,
    pub lr: u64,
    pub fp: u64,
    // used by DWARF CFI
    #[cfg(feature = "profiler-framehop")]
    pub sp: u64,
}

pub trait Unwinder {
    fn unwind(&mut self, regs: UnwindRegs, f: impl FnMut(u64)) -> Result<()>;
}
