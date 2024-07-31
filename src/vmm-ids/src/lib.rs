use std::sync::Arc;

use gruel::{MultiShutdownSignal, SignalChannel};
use newt::define_num_enum;

pub type VmmShutdownSignal = MultiShutdownSignal<VmmShutdownPhase>;

define_num_enum! {
    pub enum VmmShutdownPhase {
        /// Pauses execution of all vCPUs, letting them wait to destroy themselves. This should
        /// happen before everything else because vCPUs exiting the loop could actually initiate the
        /// shutdown.
        VcpuExitLoop,

        /// Shut-down the serial console.
        Console,

        /// Shut-down virtio devices.
        Devices,

        /// Destroy all the vCPUs.
        VcpuDestroy,

        /// Destroy the virtualization framework.
        HvfDestroy,
    }
}

pub type ArcVcpuSignal = Arc<VcpuSignal>;
pub type VcpuSignal = SignalChannel<VcpuSignalMask>;

bitflags::bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub struct VcpuSignalMask: u64 {
        /// Exit the main loop without tearing down the vCPU.
        const EXIT_LOOP = 1 << 0;

        /// Destroy the vCPU. This should not be signalled without first signalling `EXIT_LOOP`.
        const DESTROY_VM = 1 << 1;

        /// Wake-up the vCPU for an interrupt. This is only used on `aarch64` for the GIC.
        #[cfg(target_arch = "aarch64")]
        const INTERRUPT = 1 << 2;

        /// Pause the vCPU for a balloon operation.
        const PAUSE = 1 << 3;

        // Wake up the vCPU to acquire a PV spinlock that's now unlocked.
        #[cfg(target_arch = "aarch64")]
        const PVLOCK = 1 << 4;

        /// Dump the vCPU state for debugging.
        #[cfg(target_arch = "aarch64")]
        const DUMP_DEBUG = 1 << 5;

        /// Take a sample for profiling.
        const PROFILER_SAMPLE = 1 << 6;

        // TODO: We might actually just not want this.
        const ANY_SHUTDOWN = Self::EXIT_LOOP.bits() | Self::DESTROY_VM.bits();

        // Most waiters can be woken up by any signal except PVLOCK (which is a token only consumed by PvlockWait).
        #[cfg(target_arch = "aarch64")]
        const ALL_WAIT = Self::EXIT_LOOP.bits() | Self::DESTROY_VM.bits() | Self::PAUSE.bits() | Self::INTERRUPT.bits() | Self::DUMP_DEBUG.bits();
        #[cfg(not(target_arch = "aarch64"))]
        const ALL_WAIT = Self::EXIT_LOOP.bits() | Self::DESTROY_VM.bits() | Self::PAUSE.bits();
    }
}
