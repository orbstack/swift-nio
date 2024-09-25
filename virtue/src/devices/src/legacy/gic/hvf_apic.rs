use hvf::HvfVm;

use super::{GicImpl, WfeThread};

pub struct HvfApic {
    hvf_vm: HvfVm,
}

impl HvfApic {
    pub fn new(hvf_vm: HvfVm) -> Self {
        Self { hvf_vm }
    }
}

impl GicImpl for HvfApic {
    // === MMIO === //

    fn get_addr(&self) -> u64 {
        arch::x86_64::mptable::APIC_DEFAULT_PHYS_BASE as u64
    }

    fn get_size(&self) -> u64 {
        4096
    }

    fn read(&self, _vcpuid: u64, _offset: u64, _data: &mut [u8]) {
        unimplemented!()
    }

    fn write(&self, _vcpuid: u64, _offset: u64, _data: &[u8]) {
        unimplemented!()
    }

    // === IRQ Assertion === //

    fn set_irq(&self, _vcpuid: Option<u64>, irq_line: u32) {
        debug!("asserting ioapic irq {}", irq_line);
        self.hvf_vm.assert_ioapic_irq(irq_line as i32).unwrap();
    }

    // === VCPU management === //

    fn register_vcpu(&self, _vcpuid: u64, _wfe_thread: WfeThread) {
        unimplemented!()
    }

    fn get_vcpu_handle(&self, _vcpuid: u64) -> Box<dyn super::GicVcpuHandle> {
        unimplemented!()
    }

    fn kick_vcpu_for_pvlock(&self, _vcpuid: u64) {
        unimplemented!()
    }
}
