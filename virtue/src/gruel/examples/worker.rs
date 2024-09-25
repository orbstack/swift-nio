use std::{sync::Arc, thread, time::Duration};

use bitflags::bitflags;
use gruel::{
    define_waker_set, BoundSignalChannel, MultiShutdownSignal, MultiShutdownSignalExt,
    ParkSignalChannelExt, ParkWaker, ShutdownAlreadyRequestedExt, SignalChannel,
};
use newt::define_num_enum;

define_waker_set! {
    #[derive(Default)]
    struct VcpuWakerSet {
        park: ParkWaker,
    }
}

fn main() {
    let stopper = MultiShutdownSignal::new();
    let signal_1 = Arc::new(SignalChannel::new(VcpuWakerSet::default()));
    let signal_2 = Arc::new(SignalChannel::new(VcpuWakerSet::default()));

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

fn vcpu_worker(
    stopper: MultiShutdownSignal<VmStopPhase>,
    signal: Arc<SignalChannel<VcpuSignal, VcpuWakerSet>>,
) {
    let _shutdown_task = stopper
        .spawn_signal(
            VmStopPhase::DestroyVcpu,
            BoundSignalChannel::new(signal.clone(), VcpuSignal::STOP),
        )
        .unwrap_or_run_now();

    loop {
        if !signal.take(VcpuSignal::STOP).is_empty() {
            break;
        }

        if !signal.take(VcpuSignal::PANIC).is_empty() {
            panic!("Ruh roh!");
        }

        signal.wait_on_park(VcpuSignal::all());
    }
}
