// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

use gicv3::device::InterruptId;
use utils::Mutex;
use vmm_ids::ArcVcpuSignal;

use hvf::{HvVcpuRef, HvfVm};

use crate::bus::BusDevice;

#[cfg(target_arch = "x86_64")]
use super::hvf_apic::HvfApic;

#[derive(Debug)]
pub struct WfeThread {
    pub hv_vcpu: HvVcpuRef,
    pub signal: ArcVcpuSignal,
}

pub struct Gic(Box<dyn UserspaceGicImpl>);

impl Gic {
    #[cfg(target_arch = "aarch64")]
    pub fn new(hvf_vm: &HvfVm) -> Self {
        use super::hvf_gic::HvfGic;

        if hvf_vm.gic_props.is_some() {
            Self(Box::new(HvfGic::new(hvf_vm.clone())))
        } else {
            Self(Box::<super::v3::UserspaceGicV3>::default())
        }
    }

    #[cfg(target_arch = "x86_64")]
    pub fn new(hvf_vm: &HvfVm) -> Self {
        Self(Box::new(HvfApic::new(hvf_vm.clone())))
    }

    pub fn get_addr(&self) -> u64 {
        self.0.get_addr()
    }

    pub fn get_size(&self) -> u64 {
        self.0.get_size()
    }

    pub fn set_irq(&mut self, irq_line: u32) {
        self.0.set_irq(irq_line)
    }

    pub fn register_vcpu(&mut self, vcpuid: u64, wfe_thread: WfeThread) {
        self.0.register_vcpu(vcpuid, wfe_thread)
    }

    pub fn get_vcpu_handle(&mut self, vcpuid: u64) -> Box<dyn GicVcpuHandle> {
        self.0.get_vcpu_handle(vcpuid)
    }

    pub fn kick_vcpu_for_pvlock(&mut self, vcpuid: u64) {
        self.0.kick_vcpu_for_pvlock(vcpuid);
    }
}

impl BusDevice for Gic {
    fn read(&mut self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.0.read(vcpuid, offset, data)
    }

    fn write(&mut self, vcpuid: u64, offset: u64, data: &[u8]) {
        self.0.write(vcpuid, offset, data)
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        self.0.iter_sysregs()
    }

    fn read_sysreg(&mut self, vcpuid: u64, reg: u64) -> u64 {
        self.0.read_sysreg(vcpuid, reg)
    }

    fn write_sysreg(&mut self, vcpuid: u64, reg: u64, value: u64) {
        self.0.write_sysreg(vcpuid, reg, value)
    }
}

pub trait GicVcpuHandle: Send + Sync {
    fn get_pending_irq(&mut self, gic: &Mutex<Gic>) -> Option<InterruptId>;

    fn should_wait(&mut self, gic: &Mutex<Gic>) -> bool;

    fn set_vtimer_irq(&mut self);
}

pub trait UserspaceGicImpl: 'static + Send {
    // === MMIO === //

    fn get_addr(&self) -> u64;

    fn get_size(&self) -> u64;

    fn read(&mut self, vcpuid: u64, offset: u64, data: &mut [u8]);

    fn write(&mut self, vcpuid: u64, offset: u64, data: &[u8]);

    // === Sysregs === //

    fn iter_sysregs(&self) -> Vec<u64> {
        Vec::new()
    }

    fn read_sysreg(&mut self, vcpuid: u64, reg: u64) -> u64 {
        let _ = (vcpuid, reg);
        unimplemented!()
    }

    fn write_sysreg(&mut self, vcpuid: u64, reg: u64, value: u64) {
        let _ = (vcpuid, reg, value);
        unimplemented!()
    }

    // === IRQ Assertion === //

    fn set_irq(&mut self, irq_line: u32);

    // === VCPU management === //

    fn register_vcpu(&mut self, vcpuid: u64, wfe_thread: WfeThread);

    fn get_vcpu_handle(&mut self, vcpuid: u64) -> Box<dyn GicVcpuHandle>;

    // TODO: This probably shouldn't be here.
    fn kick_vcpu_for_pvlock(&mut self, vcpuid: u64);
}
