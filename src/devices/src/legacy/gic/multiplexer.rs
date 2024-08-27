// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.inner

use std::sync::Arc;

use gicv3::device::InterruptId;
use vmm_ids::ArcVcpuSignal;

use hvf::{HvVcpuRef, HvfVm};

use crate::{ErasedBusDevice, LocklessBusDevice};

#[cfg(target_arch = "x86_64")]
use super::hvf_apic::HvfApic;

// === Multiplexer === //

#[derive(Debug)]
pub struct WfeThread {
    pub hv_vcpu: HvVcpuRef,
    pub signal: ArcVcpuSignal,
}

pub struct Gic {
    inner: Box<dyn GicImpl>,
}

impl Gic {
    #[cfg(target_arch = "aarch64")]
    pub fn new(hvf_vm: &Arc<HvfVm>) -> Self {
        use super::hvf_gic::HvfGic;

        if hvf_vm.gic_props.is_some() {
            Self {
                inner: Box::new(HvfGic::new(hvf_vm.clone())),
            }
        } else {
            Self {
                inner: Box::<super::v3::UserspaceGicV3>::default(),
            }
        }
    }

    #[cfg(target_arch = "x86_64")]
    pub fn new(hvf_vm: &HvfVm) -> Self {
        Self(Box::new(HvfApic::new(hvf_vm.clone())))
    }

    pub fn get_addr(&self) -> u64 {
        self.inner.get_addr()
    }

    pub fn get_size(&self) -> u64 {
        self.inner.get_size()
    }

    pub fn set_irq(&self, irq_line: u32) {
        self.set_irq_for_vcpu(None, irq_line)
    }

    pub fn set_irq_for_vcpu(&self, vcpuid: Option<u64>, irq_line: u32) {
        self.inner.set_irq(vcpuid, irq_line)
    }

    pub fn register_vcpu(&self, vcpuid: u64, wfe_thread: WfeThread) {
        self.inner.register_vcpu(vcpuid, wfe_thread)
    }

    pub fn get_vcpu_handle(&self, vcpuid: u64) -> Box<dyn GicVcpuHandle> {
        self.inner.get_vcpu_handle(vcpuid)
    }

    pub fn kick_vcpu_for_pvlock(&self, vcpuid: u64) {
        self.inner.kick_vcpu_for_pvlock(vcpuid);
    }
}

#[derive(Clone)]
pub struct GicBusDevice(pub Arc<Gic>);

impl LocklessBusDevice for GicBusDevice {
    fn clone_erased(&self) -> ErasedBusDevice {
        ErasedBusDevice::new(self.clone())
    }

    fn read(&self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.0.inner.read(vcpuid, offset, data)
    }

    fn write(&self, vcpuid: u64, offset: u64, data: &[u8]) {
        self.0.inner.write(vcpuid, offset, data)
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        self.0.inner.iter_sysregs()
    }

    fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        self.0.inner.read_sysreg(vcpuid, reg)
    }

    fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        self.0.inner.write_sysreg(vcpuid, reg, value)
    }
}

// === Generic Device Traits === //

pub trait GicImpl: 'static + Send + Sync {
    // === MMIO === //

    fn get_addr(&self) -> u64;

    fn get_size(&self) -> u64;

    fn read(&self, vcpuid: u64, offset: u64, data: &mut [u8]);

    fn write(&self, vcpuid: u64, offset: u64, data: &[u8]);

    // === Sysregs === //

    fn iter_sysregs(&self) -> Vec<u64> {
        Vec::new()
    }

    fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        let _ = (vcpuid, reg);
        unimplemented!()
    }

    fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        let _ = (vcpuid, reg, value);
        unimplemented!()
    }

    // === IRQ Assertion === //

    fn set_irq(&self, vcpuid: Option<u64>, irq_line: u32);

    // === VCPU management === //

    fn register_vcpu(&self, vcpuid: u64, wfe_thread: WfeThread);

    fn get_vcpu_handle(&self, vcpuid: u64) -> Box<dyn GicVcpuHandle>;

    // TODO: This probably shouldn't be here.
    fn kick_vcpu_for_pvlock(&self, vcpuid: u64);
}

pub trait GicVcpuHandle: Send + Sync {
    fn get_pending_irq(&mut self, gic: &Gic) -> Option<InterruptId>;

    fn should_wait(&mut self, gic: &Gic) -> bool;

    fn set_vtimer_irq(&mut self);
}
