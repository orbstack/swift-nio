use counter::RateCounter;
use hvf::{HvfVcpu, VcpuId};
use rustc_hash::FxHashMap;
use std::sync::{Arc, RwLock};
use utils::Mutex;
use vmm_ids::VcpuSignalMask;

use super::{Gic, GicImpl, GicVcpuHandle, WfeThread};

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
    wfe_threads: RwLock<FxHashMap<PeId, WfeThread>>,
}

const TIMER_INT_ID: InterruptId = InterruptId(GTIMER_VIRT + 16);

impl GicImpl for UserspaceGicV3 {
    fn get_addr(&self) -> u64 {
        UserspaceGICv3::mapped_range().start
    }

    fn get_size(&self) -> u64 {
        UserspaceGICv3::mapped_range().size()
    }

    fn read(&self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.gic.read(
            &mut HvfGicEventHandler {
                wfe_threads: &self.wfe_threads,
            },
            PeId(vcpuid),
            MmioRequest::new(offset, data),
        );
    }

    fn write(&self, vcpuid: u64, offset: u64, data: &[u8]) {
        self.gic.write(PeId(vcpuid), MmioRequest::new(offset, data));
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        GicSysReg::VARIANTS.map(|v| v as u64).to_vec()
    }

    fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        self.gic.read_sysreg(
            &mut HvfGicEventHandler {
                wfe_threads: &self.wfe_threads,
            },
            PeId(vcpuid),
            GicSysReg::parse(reg),
        )
    }

    fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        self.gic.write_sysreg(
            &mut HvfGicEventHandler {
                wfe_threads: &self.wfe_threads,
            },
            PeId(vcpuid),
            GicSysReg::parse(reg),
            value,
        );
    }

    fn set_irq(&self, vcpuid: Option<u64>, irq_line: u32) {
        self.gic.send_spi(
            &mut HvfGicEventHandler {
                wfe_threads: &self.wfe_threads,
            },
            vcpuid.map(PeId),
            InterruptId(irq_line),
        );
    }

    fn register_vcpu(&self, vcpuid: u64, wfe_thread: WfeThread) {
        tracing::trace!("v3::register_vcpu({vcpuid}, {wfe_thread:?})");
        self.wfe_threads
            .write()
            .unwrap()
            .insert(PeId(vcpuid), wfe_thread);
    }

    fn get_vcpu_handle(&self, vcpuid: u64) -> Box<dyn GicVcpuHandle> {
        Box::new(GicV3VcpuHandle(self.gic.pe_state(
            &mut HvfGicEventHandler {
                wfe_threads: &self.wfe_threads,
            },
            PeId(vcpuid),
            |_, state| state.int_state.clone(),
        )))
    }

    fn kick_vcpu_for_pvlock(&self, vcpuid: u64) {
        HvfGicEventHandler {
            wfe_threads: &self.wfe_threads,
        }
        .kick_vcpu_for_pvlock(PeId(vcpuid));
    }
}

struct HvfGicEventHandler<'a> {
    wfe_threads: &'a RwLock<FxHashMap<PeId, WfeThread>>,
}

impl GicV3EventHandler for HvfGicEventHandler<'_> {
    fn kick_vcpu_for_irq(&mut self, pe: PeId) {
        self.wfe_threads
            .read()
            .unwrap()
            .get(&pe)
            .unwrap()
            .signal
            .assert(VcpuSignalMask::IRQ);

        COUNT_VCPU_KICK.count();
    }

    fn kick_vcpu_for_pvlock(&mut self, pe: PeId) {
        self.wfe_threads
            .read()
            .unwrap()
            .get(&pe)
            .unwrap()
            .signal
            .assert(VcpuSignalMask::PVLOCK);

        COUNT_VCPU_KICK.count();
    }

    // https://developer.arm.com/documentation/ddi0595/2021-12/AArch64-Registers/MPIDR-EL1--Multiprocessor-Affinity-Register
    fn get_affinity(&mut self, pe: PeId) -> Affinity {
        let mpidr = BitPack(VcpuId(pe.0).to_mpidr());
        let aff3 = mpidr.get_range(32, 39);
        let aff2 = mpidr.get_range(16, 23);
        let aff1 = mpidr.get_range(8, 15);
        let aff0 = mpidr.get_range(0, 7);

        Affinity([aff0 as u8, aff1 as u8, aff2 as u8, aff3 as u8])
    }

    fn handle_custom_eoi(&mut self, pe: PeId, int_id: InterruptId) {
        if int_id == TIMER_INT_ID {
            let wfe_threads = self.wfe_threads.read().unwrap();
            HvfVcpu::set_vtimer_masked_static(wfe_threads[&pe].hv_vcpu, false).unwrap();
        }
    }
}

struct GicV3VcpuHandle(Arc<Mutex<PeInterruptState>>);

impl GicVcpuHandle for GicV3VcpuHandle {
    fn get_pending_irq(&mut self, _gic: &Gic) -> Option<InterruptId> {
        self.0.lock().unwrap().get_pending_irq()
    }

    fn should_wait(&mut self, _gic: &Gic) -> bool {
        !self.0.lock().unwrap().is_irq_line_asserted()
    }

    fn set_vtimer_irq(&mut self) {
        self.0.lock().unwrap().push_local_interrupt(TIMER_INT_ID);
    }
}
