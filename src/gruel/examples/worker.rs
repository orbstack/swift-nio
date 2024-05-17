use std::{thread, time::Duration};

use bitflags::bitflags;
use gruel::{
    MultiShutdownSignal, MultiShutdownSignalExt, ParkSignalChannelExt, ShutdownAlreadyRequestedExt,
    SignalChannel,
};
use newt::define_num_enum;

fn main() {
    let stopper = MultiShutdownSignal::new();
    let signal_1 = SignalChannel::new();
    let signal_2 = SignalChannel::new();

    thread::spawn({
        let stopper = stopper.clone();
        let signal = signal_1.clone();
        move || vcpu_worker(stopper, signal)
    });

    thread::spawn({
        let stopper = stopper.clone();
        let signal = signal_2.clone();
        move || vcpu_worker(stopper, signal)
    });

    thread::sleep(Duration::from_secs(1));
    signal_1.assert(VcpuSignal::PANIC);
    thread::sleep(Duration::from_secs(1));
    stopper.shutdown();
}

define_num_enum! {
    pub enum VmStopPhase {
        StopDevices,
        StopGic,
        DestroyVcpu,
    }
}

bitflags! {
    #[derive(Copy, Clone)]
    pub struct VcpuSignal: u64 {
        const STOP = 1 << 0;
        const PANIC = 1 << 1;
    }
}

fn vcpu_worker(stopper: MultiShutdownSignal<VmStopPhase>, signal: SignalChannel<VcpuSignal>) {
    let _shutdown_task = stopper
        .spawn_signal(VmStopPhase::DestroyVcpu, signal.bind_ref(VcpuSignal::STOP))
        .unwrap_or_run_now();

    loop {
        if !signal.take(VcpuSignal::STOP).is_empty() {
            break;
        }

        if !signal.take(VcpuSignal::PANIC).is_empty() {
            panic!("Ruh roh!");
        }

        signal.wait_on_park(VcpuSignal::STOP | VcpuSignal::PANIC);
    }
}
