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

        /// Notify the `libkrun` worker that it can close its worker thread and notify the
        /// embedder.
        NotifyLibkrunWorker,
    }
}

pub type VcpuSignal = SignalChannel<VcpuSignalMask>;

bitflags::bitflags! {
    #[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
    pub struct VcpuSignalMask: u64 {
        const EXIT_LOOP = 1 << 0;
        const DESTROY_VM = 1 << 1;
        const INTERRUPT = 1 << 2;

        const ANY_SHUTDOWN = Self::EXIT_LOOP.bits() | Self::DESTROY_VM.bits();
    }
}
