use std::sync::Arc;

use hvf::HvfVm;
use rustc_hash::FxHashMap;

use super::{GicImpl, WfeThread};

pub struct HvfGic {
    hvf_vm: Arc<HvfVm>,
    vcpus: FxHashMap<u64, WfeThread>,
}

impl HvfGic {
    pub fn new(hvf_vm: Arc<HvfVm>) -> Self {
        Self {
            hvf_vm,
            vcpus: FxHashMap::default(),
        }
    }
}

impl GicImpl for HvfGic {
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

    fn set_irq(&mut self, _vcpuid: Option<u64>, irq_line: u32) {
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

    fn kick_vcpu_for_pvlock(&mut self, _vcpuid: u64) {
        todo!();
    }
}

struct HvfGicHandle {}

impl super::GicVcpuHandle for HvfGicHandle {
    fn get_pending_irq(&mut self, _gic: &super::Gic) -> Option<gicv3::device::InterruptId> {
        None
    }

    fn should_wait(&mut self, _gic: &super::Gic) -> bool {
        unimplemented!();
    }

    fn set_vtimer_irq(&mut self) {
        unimplemented!();
    }
}
