use gruel::MultiShutdownSignal;
use newt::define_num_enum;

pub type VmmShutdownSignal = MultiShutdownSignal<VmmShutdownPhase>;

define_num_enum! {
    pub enum VmmShutdownPhase {
        /// Pauses execution of all vCPUs, letting them wait to destroy themselves.
        VcpuPause,

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
