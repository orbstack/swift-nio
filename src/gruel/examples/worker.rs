use bitflags::bitflags;
use gruel::{BoundSignalChannel, MultiShutdownSignal, MultiShutdownSignalExt, SignalChannel};
use newt::define_num_enum;

fn main() {}

define_num_enum! {
    pub enum VmStopPhase {
        StopDevices,
        StopGic,
        DestroyVcpu,
    }
}

bitflags! {
    pub struct VcpuSignal: u64 {
        const STOP = 1 << 0;
    }
}

fn vcpu_worker(stopper: MultiShutdownSignal<VmStopPhase>) {
    let signal = SignalChannel::new();
    let _shutdown_task = stopper.spawn_signal(
        VmStopPhase::DestroyVcpu,
        BoundSignalChannel::wrap(signal.clone(), VcpuSignal::STOP),
    );

    // TODO
}
