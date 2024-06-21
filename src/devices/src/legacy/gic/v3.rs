use counter::RateCounter;
use rustc_hash::FxHashMap;
use std::sync::Arc;
use utils::Mutex;
use vmm_ids::VcpuSignalMask;

use super::{Gic, GicVcpuHandle, UserspaceGicImpl, WfeThread};

use arch::aarch64::{gicv3::UserspaceGICv3, layout::GTIMER_VIRT};
pub use gicv3::mmio::GicSysReg;
use gicv3::{
    device::{Affinity, GicV3EventHandler, InterruptId, PeId, PeInterruptState},
    mmio_util::{BitPack, MmioRequest},
};

counter::counter! {
    COUNT_VCPU_KICK in "gic.vcpu.kick": RateCounter = RateCounter::new(FILTER);
}

#[derive(Default)]
pub struct UserspaceGicV3 {
    gic: gicv3::device::GicV3,
    wfe_threads: FxHashMap<PeId, WfeThread>,
}

const TIMER_INT_ID: InterruptId = InterruptId(GTIMER_VIRT + 16);

impl UserspaceGicImpl for UserspaceGicV3 {
    fn get_addr(&self) -> u64 {
        UserspaceGICv3::mapped_range().start
    }

    fn get_size(&self) -> u64 {
        UserspaceGICv3::mapped_range().size()
    }

    fn read(&mut self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.gic.read(
            &mut HvfGicEventHandler {
                wfe_threads: &mut self.wfe_threads,
            },
            PeId(vcpuid),
            MmioRequest::new(offset, data),
        );
    }

    fn write(&mut self, vcpuid: u64, offset: u64, data: &[u8]) {
        self.gic.write(PeId(vcpuid), MmioRequest::new(offset, data));
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        GicSysReg::VARIANTS.map(|v| v as u64).to_vec()
    }

    fn read_sysreg(&mut self, vcpuid: u64, reg: u64) -> u64 {
        self.gic.read_sysreg(
            &mut HvfGicEventHandler {
                wfe_threads: &mut self.wfe_threads,
            },
            PeId(vcpuid),
            GicSysReg::parse(reg),
        )
    }

    fn write_sysreg(&mut self, vcpuid: u64, reg: u64, value: u64) {
        self.gic.write_sysreg(
            &mut HvfGicEventHandler {
                wfe_threads: &mut self.wfe_threads,
            },
            PeId(vcpuid),
            GicSysReg::parse(reg),
            value,
        );
    }

    fn set_irq(&mut self, irq_line: u32) {
        self.gic.send_spi(
            &mut HvfGicEventHandler {
                wfe_threads: &mut self.wfe_threads,
            },
            InterruptId(irq_line),
        );
    }

    fn register_vcpu(&mut self, vcpuid: u64, wfe_thread: WfeThread) {
        tracing::trace!("v3::register_vcpu({vcpuid}, {wfe_thread:?})");
        self.wfe_threads.insert(PeId(vcpuid), wfe_thread);
    }

    fn get_vcpu_handle(&mut self, vcpuid: u64) -> Box<dyn GicVcpuHandle> {
        Box::new(GicV3VcpuHandle(
            self.gic
                .pe_state(
                    &mut HvfGicEventHandler {
                        wfe_threads: &mut self.wfe_threads,
                    },
                    PeId(vcpuid),
                )
                .int_state
                .clone(),
        ))
    }

    fn kick_vcpu(&mut self, vcpuid: u64) {
        HvfGicEventHandler {
            wfe_threads: &mut self.wfe_threads,
        }
        .kick_vcpu_for_irq(PeId(vcpuid));
    }
}

struct HvfGicEventHandler<'a> {
    wfe_threads: &'a mut FxHashMap<PeId, WfeThread>,
}

impl GicV3EventHandler for HvfGicEventHandler<'_> {
    fn kick_vcpu_for_irq(&mut self, pe: PeId) {
        self.wfe_threads
            .get(&pe)
            .unwrap()
            .signal
            .assert(VcpuSignalMask::INTERRUPT);

        COUNT_VCPU_KICK.count();
    }

    // https://developer.arm.com/documentation/ddi0595/2021-12/AArch64-Registers/MPIDR-EL1--Multiprocessor-Affinity-Register
    fn get_affinity(&mut self, pe: PeId) -> Affinity {
        let mpidr = BitPack(hvf::vcpu_id_to_mpidr(pe.0));
        let aff3 = mpidr.get_range(32, 39);
        let aff2 = mpidr.get_range(16, 23);
        let aff1 = mpidr.get_range(8, 15);
        let aff0 = mpidr.get_range(0, 7);

        Affinity([aff0 as u8, aff1 as u8, aff2 as u8, aff3 as u8])
    }

    fn handle_custom_eoi(&mut self, pe: PeId, int_id: InterruptId) {
        if int_id == TIMER_INT_ID {
            let waker = self.wfe_threads.get(&pe).unwrap();
            hvf::vcpu_set_vtimer_mask(waker.hv_vcpu, false).unwrap();
        }
    }
}

struct GicV3VcpuHandle(Arc<Mutex<PeInterruptState>>);

impl GicVcpuHandle for GicV3VcpuHandle {
    fn get_pending_irq(&mut self, _gic: &Mutex<Gic>) -> Option<InterruptId> {
        self.0.lock().unwrap().get_pending_irq()
    }

    fn should_wait(&mut self, _gic: &Mutex<Gic>) -> bool {
        !self.0.lock().unwrap().is_irq_line_asserted()
    }

    fn set_vtimer_irq(&mut self) {
        self.0.lock().unwrap().push_interrupt(TIMER_INT_ID);
    }
}
