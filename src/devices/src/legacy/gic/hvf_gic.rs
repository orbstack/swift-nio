use hvf::HvfVm;
use rustc_hash::FxHashMap;
use vmm_ids::VcpuSignalMask;

use super::{UserspaceGicImpl, WfeThread};

pub struct HvfGic {
    hvf_vm: HvfVm,
    vcpus: FxHashMap<u64, WfeThread>,
}

impl HvfGic {
    pub fn new(hvf_vm: HvfVm) -> Self {
        Self {
            hvf_vm,
            vcpus: FxHashMap::default(),
        }
    }
}

impl UserspaceGicImpl for HvfGic {
    // === MMIO === //

    fn get_addr(&self) -> u64 {
        let props = self.hvf_vm.gic_props.as_ref().unwrap();
        props.redist_base
    }

    fn get_size(&self) -> u64 {
        let props = self.hvf_vm.gic_props.as_ref().unwrap();
        props.dist_size + props.redist_total_size
    }

    fn read(&mut self, _vcpuid: u64, _offset: u64, _data: &mut [u8]) {
        todo!()
    }

    fn write(&mut self, _vcpuid: u64, _offset: u64, _data: &[u8]) {
        todo!()
    }

    // === IRQ Assertion === //

    fn set_irq(&mut self, irq_line: u32) {
        debug!("asserting gic irq {}", irq_line);
        self.hvf_vm.assert_spi(irq_line).unwrap();
    }

    // === VCPU management === //

    fn register_vcpu(&mut self, vcpuid: u64, wfe_thread: WfeThread) {
        // we still need to save these for kick_vcpu to work
        self.vcpus.insert(vcpuid, wfe_thread);
    }

    fn get_vcpu_handle(&mut self, _vcpuid: u64) -> Box<dyn super::GicVcpuHandle> {
        Box::new(HvfGicHandle {})
    }

    fn kick_vcpu(&mut self, _vcpuid: u64) {
        todo!();
    }
}

struct HvfGicHandle {}

impl super::GicVcpuHandle for HvfGicHandle {
    fn get_pending_irq(
        &mut self,
        _gic: &utils::Mutex<super::Gic>,
    ) -> Option<gicv3::device::InterruptId> {
        None
    }

    fn should_wait(&mut self, _gic: &utils::Mutex<super::Gic>) -> bool {
        unimplemented!();
    }

    fn set_vtimer_irq(&mut self) {
        unimplemented!();
    }
}
