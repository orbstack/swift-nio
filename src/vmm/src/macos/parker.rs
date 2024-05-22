use std::sync::Mutex;

use gruel::{StartupAbortedError, StartupSignal, StartupTask};
use hvf::HvfVm;
use vmm_ids::{VcpuSignal, VcpuSignalMask};

pub struct VmParker {
    hvf_vm: HvfVm,
    vcpus: Mutex<Vec<VcpuSignal>>,
    park_signal: StartupSignal,
    unpark_signal: StartupSignal,
    regions: Mutex<Vec<MapRegion>>,
}

pub struct MapRegion {
    host_start_addr: u64,
    guest_start_addr: u64,
    size: u64,
}

impl VmParker {
    pub fn new(hvf_vm: HvfVm) -> Self {
        Self {
            hvf_vm,
            vcpus: Default::default(),
            park_signal: Default::default(),
            unpark_signal: Default::default(),
            regions: Default::default(),
        }
    }

    pub fn register_vcpu(&self, vcpu: VcpuSignal) {
        self.vcpus.lock().unwrap().push(vcpu);
    }

    pub fn park(&self) -> Result<StartupTask, StartupAbortedError> {
        // Resurrect the unpark task. We do this here to ensure that parking vCPUs don't
        // immediately exit.
        let unpark_task = self.unpark_signal.resurrect_cloned();

        // Let's send a pause signal to every vCPU. They will receive and honor this since this
        // signal is never asserted outside of `park` (or when the signal is aborted)
        for cpu in &*self.vcpus.lock().unwrap() {
            cpu.assert(VcpuSignalMask::PAUSE);
        }

        // Now, wait for every vCPU to enter the parked state. If a shutdown occurs, this signal will
        // be aborted and we'll unblock naturally.
        self.park_signal
            .wait()
            // From there, we just have to give the consumer the unpark task so they can eventually
            // resolve it.
            .map(|_| unpark_task)
    }

    pub fn process_park_commands(
        &self,
        signal: &VcpuSignal,
        park_task: StartupTask,
    ) -> Result<StartupTask, StartupAbortedError> {
        // Check whether the signal needs to be resolved.
        if signal.take(VcpuSignalMask::PAUSE).is_empty() {
            return Ok(park_task);
        }

        // Unmap our memory. We do this here rather than on the parking side since the parking side
        // could very easily crash.
        let regions = self.regions.lock().unwrap();
        for region in regions.iter() {
            debug!(
                "unmap_memory: {:x} {:x}",
                region.guest_start_addr, region.size
            );
            self.hvf_vm
                .unmap_memory(region.guest_start_addr, region.size)
                .unwrap();
        }

        // Tell the parker that we successfully parked.
        let park_task = park_task.success_keeping();

        // Now, we really need to wait on the unpark signal.
        self.unpark_signal.wait()?;

        // Remap our memory. Again, we do this here since the parking thread could have crashed.
        let regions = self.regions.lock().unwrap();
        for region in regions.iter() {
            debug!(
                "map_memory: {:x} {:x} {:x}",
                region.host_start_addr, region.guest_start_addr, region.size
            );
            self.hvf_vm
                .map_memory(region.host_start_addr, region.guest_start_addr, region.size)
                .unwrap();
        }

        // And we're back in business!
        Ok(park_task.resurrect())
    }
}
