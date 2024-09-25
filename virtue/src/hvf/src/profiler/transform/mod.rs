use std::collections::VecDeque;

use super::SymbolicatedFrame;

pub trait StackTransform {
    fn transform(&self, stack: &mut VecDeque<SymbolicatedFrame>) -> anyhow::Result<()>;
}

mod cgo;
mod host_syscall;
mod leaf_call;
mod linux_irq;

pub use cgo::CgoTransform;
pub use host_syscall::HostSyscallTransform;
pub use leaf_call::LeafCallTransform;
pub use linux_irq::LinuxIrqTransform;
