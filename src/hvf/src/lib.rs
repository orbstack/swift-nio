use std::sync::Arc;

use crossbeam_channel::Sender;
use gruel::{mpsc::SignalMpsc, SignalChannel, StartupAbortedError, StartupTask, WakerSet};
use profiler::{
    PartialSample, Profiler, ProfilerGuestContext, ProfilerVcpuInit, VcpuProfilerResults,
};
use vmm_ids::{ArcVcpuSignal, VcpuSignalMask};

#[cfg(target_arch = "x86_64")]
mod x86_64;
#[cfg(target_arch = "x86_64")]
pub use x86_64::*;

#[cfg(target_arch = "aarch64")]
mod aarch64;
#[cfg(target_arch = "aarch64")]
pub use aarch64::*;

pub mod memory;
pub mod profiler;

pub struct VcpuProfilerState {
    pub profiler: Arc<Profiler>,
    histograms: profiler::VcpuHistograms,
}

impl VcpuProfilerState {
    pub fn new(profiler: Arc<Profiler>) -> anyhow::Result<Self> {
        Ok(Self {
            profiler,
            histograms: profiler::VcpuHistograms::new()?,
        })
    }

    pub fn finish(self, sender: Sender<VcpuProfilerResults>) -> anyhow::Result<()> {
        sender.send(VcpuProfilerResults {
            histograms: self.histograms,
        })?;
        Ok(())
    }
}

pub struct VcpuHandleInner {
    signal: ArcVcpuSignal,
    pub profiler_init: SignalMpsc<ProfilerVcpuInit>,
    pub profiler_sample: SignalMpsc<PartialSample>,
    pub profiler_guest_fetch: SignalMpsc<Sender<ProfilerGuestContext>>,
    pub profiler_finish: SignalMpsc<Sender<VcpuProfilerResults>>,
}

impl VcpuHandleInner {
    pub fn new<W: WakerSet>(signal: &Arc<SignalChannel<VcpuSignalMask, W>>) -> Self {
        Self {
            signal: signal.clone(),
            profiler_init: SignalMpsc::with_capacity(1, signal, VcpuSignalMask::PROFILER_INIT),
            profiler_sample: SignalMpsc::with_capacity(1, signal, VcpuSignalMask::PROFILER_SAMPLE),
            profiler_guest_fetch: SignalMpsc::with_capacity(
                1,
                signal,
                VcpuSignalMask::PROFILER_GUEST_FETCH,
            ),
            profiler_finish: SignalMpsc::with_capacity(1, signal, VcpuSignalMask::PROFILER_FINISH),
        }
    }

    pub fn pause(&self) {
        self.signal.assert(VcpuSignalMask::PAUSE);
    }

    pub fn dump_debug(&self) {
        #[cfg(target_arch = "aarch64")]
        self.signal.assert(VcpuSignalMask::DUMP_DEBUG);
        #[cfg(not(target_arch = "aarch64"))]
        tracing::error!("dump_debug not supported on this architecture");
    }
}

pub type ArcVcpuHandle = Arc<VcpuHandleInner>;

pub trait VcpuRegistry: Send + Sync {
    fn park(&self) -> Result<StartupTask, StartupAbortedError>;

    fn unpark(&self, unpark_task: StartupTask);

    fn register_vcpu(&self, id: u8, vcpu: ArcVcpuHandle) -> StartupTask;

    fn num_vcpus(&self) -> usize;

    fn get_vcpu(&self, id: u8) -> Option<ArcVcpuHandle>;

    fn vcpu_handle_park(&self, park_task: StartupTask) -> Result<StartupTask, StartupAbortedError>;

    fn dump_debug(&self);
}
