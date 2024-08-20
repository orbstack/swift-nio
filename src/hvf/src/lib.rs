use std::sync::Arc;

use crossbeam::queue::ArrayQueue;
use crossbeam_channel::Sender;
use gruel::{StartupAbortedError, StartupTask};
use profiler::{
    PartialSample, Profiler, ProfilerGuestContext, ProfilerVcpuInit, VcpuProfilerResults,
};
use utils::Mutex;
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
    profiler_init: Mutex<Option<ProfilerVcpuInit>>,
    profiler_sample: ArrayQueue<PartialSample>,
    profiler_guest_fetch: Mutex<Option<Sender<ProfilerGuestContext>>>,
    profiler_finish: Mutex<Option<Sender<VcpuProfilerResults>>>,
}

impl VcpuHandleInner {
    pub fn new(signal: ArcVcpuSignal) -> Self {
        Self {
            signal,
            profiler_init: Mutex::new(None),
            profiler_sample: ArrayQueue::new(1),
            profiler_guest_fetch: Mutex::new(None),
            profiler_finish: Mutex::new(None),
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

    pub fn send_profiler_init(&self, init: ProfilerVcpuInit) {
        *self.profiler_init.lock().unwrap() = Some(init);
        self.signal.assert(VcpuSignalMask::PROFILER_INIT);
    }

    pub fn send_profiler_sample(&self, sample: PartialSample) {
        self.profiler_sample.force_push(sample);
        self.signal.assert(VcpuSignalMask::PROFILER_SAMPLE);
    }

    pub fn send_profiler_guest_fetch(&self, sender: Sender<ProfilerGuestContext>) {
        *self.profiler_guest_fetch.lock().unwrap() = Some(sender);
        self.signal.assert(VcpuSignalMask::PROFILER_GUEST_FETCH);
    }

    pub fn send_profiler_finish(&self, sender: Sender<VcpuProfilerResults>) {
        *self.profiler_finish.lock().unwrap() = Some(sender);
        self.signal.assert(VcpuSignalMask::PROFILER_FINISH);
    }

    pub fn consume_profiler_init(&self) -> Option<ProfilerVcpuInit> {
        self.profiler_init.lock().unwrap().take()
    }

    pub fn consume_profiler_sample(&self) -> Option<PartialSample> {
        self.profiler_sample.pop()
    }

    pub fn consume_profiler_guest_fetch(&self) -> Option<Sender<ProfilerGuestContext>> {
        self.profiler_guest_fetch.lock().unwrap().take()
    }

    pub fn consume_profiler_finish(&self) -> Option<Sender<VcpuProfilerResults>> {
        self.profiler_finish.lock().unwrap().take()
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
