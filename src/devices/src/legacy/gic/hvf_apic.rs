// Copyright 2021 Red Hat, Inc.
// SPDX-License-Identifier: Apache-2.0

use std::collections::btree_map::Entry;
use std::thread::Thread;

use super::{UserspaceGicImpl, WfeThread};

const IRQ_NUM: u32 = 64;
const MAX_CPUS: u64 = 8;

enum VcpuStatus {
    Running,
    Waiting,
}

struct VcpuInfo {
    status: VcpuStatus,
    wfe_thread: Thread,
}

pub struct HvfApic {}

impl Default for HvfApic {
    fn default() -> Self {
        Self {}
    }
}

impl HvfApic {}

impl UserspaceGicImpl for HvfApic {
    // === MMIO === //

    fn get_addr(&self) -> u64 {
        arch::x86_64::mptable::APIC_DEFAULT_PHYS_BASE as u64
    }

    fn get_size(&self) -> u64 {
        // TODO
        4096
    }

    fn read(&mut self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        todo!()
    }

    fn write(&mut self, vcpuid: u64, offset: u64, data: &[u8]) {
        todo!()
    }

    // === IRQ Assertion === //

    fn set_vtimer_irq(&mut self, vcpuid: u64) {
        todo!()
    }

    fn set_irq(&mut self, irq_line: u32) {
        todo!()
    }

    // === VCPU management === //

    fn register_vcpu(&mut self, vcpuid: u64, wfe_thread: WfeThread) {
        todo!()
    }

    fn vcpu_should_wait(&mut self, vcpuid: u64) -> bool {
        false
    }

    fn vcpu_has_pending_irq(&mut self, vcpuid: u64) -> bool {
        false
    }
}
