use std::sync::Arc;

use hvf::HvfVm;

use super::{GicImpl, WfeThread};

pub struct HvfGic {
    hvf_vm: Arc<HvfVm>,
}

impl HvfGic {
    pub fn new(hvf_vm: Arc<HvfVm>) -> Self {
        Self { hvf_vm }
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

    fn read(&self, _vcpuid: u64, _offset: u64, _data: &mut [u8]) {
        unimplemented!()
    }

    fn write(&self, _vcpuid: u64, _offset: u64, _data: &[u8]) {
        unimplemented!()
    }

    // === IRQ Assertion === //

    fn set_irq(&self, _vcpuid: Option<u64>, irq_line: u32) {
        debug!("asserting gic irq {}", irq_line);
        self.hvf_vm.assert_spi(irq_line).unwrap();
    }

    // === VCPU management === //

    fn register_vcpu(&self, _vcpuid: u64, _wfe_thread: WfeThread) {}

    fn get_vcpu_handle(&self, _vcpuid: u64) -> Box<dyn super::GicVcpuHandle> {
        Box::new(HvfGicHandle {})
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
