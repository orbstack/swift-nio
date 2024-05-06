// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

use hvf::HvfVm;

use super::{UserspaceGicImpl, WfeThread};

pub struct HvfApic {
    hvf_vm: HvfVm,
}

impl HvfApic {
    pub fn new(hvf_vm: HvfVm) -> Self {
        Self { hvf_vm }
    }
}

impl UserspaceGicImpl for HvfApic {
    fn as_any(&mut self) -> &mut (dyn std::any::Any + Send) {
        self
    }

    // === MMIO === //

    fn get_addr(&self) -> u64 {
        arch::x86_64::mptable::APIC_DEFAULT_PHYS_BASE as u64
    }

    fn get_size(&self) -> u64 {
        4096
    }

    fn read(&mut self, _vcpuid: u64, _offset: u64, _data: &mut [u8]) {
        todo!()
    }

    fn write(&mut self, _vcpuid: u64, _offset: u64, _data: &[u8]) {
        todo!()
    }

    // === IRQ Assertion === //

    fn set_vtimer_irq(&mut self, _vcpuid: u64) {
        todo!()
    }

    fn set_irq(&mut self, irq_line: u32) {
        debug!("asserting ioapic irq {}", irq_line);
        self.hvf_vm.assert_ioapic_irq(irq_line as i32).unwrap();
    }

    // === VCPU management === //

    fn register_vcpu(&mut self, _vcpuid: u64, _wfe_thread: WfeThread) {
        todo!()
    }

    fn get_vcpu_handle(&mut self, _vcpuid: u64) -> Box<dyn super::GicVcpuHandle> {
        unimplemented!()
    }
}
