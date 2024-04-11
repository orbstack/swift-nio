use std::{collections::HashMap, thread::Thread};

use super::UserspaceGicImpl;

use arch::aarch64::{gicv3::GICv3, layout::GTIMER_VIRT};
use gicv3::{
    device::{Affinity, GicV3EventHandler, InterruptId, PeId},
    mmio::GicSysReg,
    mmio_util::{BitPack, MmioRequest},
};

#[derive(Default)]
pub struct UserspaceGicV3 {
    gic: gicv3::device::GicV3,
    wfe_threads: HashMap<PeId, WfeThread>,
}

struct WfeThread {
    thread: Thread,
}

const TIMER_INT_ID: InterruptId = InterruptId(GTIMER_VIRT + 16);

impl UserspaceGicImpl for UserspaceGicV3 {
    fn get_addr(&self) -> u64 {
        GICv3::mapped_range().start
    }

    fn get_size(&self) -> u64 {
        GICv3::mapped_range().size()
    }

    fn read(&mut self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.gic.read(
            &mut HvfGicEventHandler {
                wakers: &mut self.wfe_threads,
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
                wakers: &mut self.wfe_threads,
            },
            PeId(vcpuid),
            GicSysReg::parse(reg),
        )
    }

    fn write_sysreg(&mut self, vcpuid: u64, reg: u64, value: u64) {
        self.gic.write_sysreg(
            &mut HvfGicEventHandler {
                wakers: &mut self.wfe_threads,
            },
            PeId(vcpuid),
            GicSysReg::parse(reg),
            value,
        );
    }

    fn set_vtimer_irq(&mut self, vcpuid: u64) {
        let vcpuid = PeId(vcpuid);
        self.gic.send_ppi(
            &mut HvfGicEventHandler {
                wakers: &mut self.wfe_threads,
            },
            vcpuid,
            TIMER_INT_ID,
        );
    }

    fn set_irq(&mut self, irq_line: u32) {
        self.gic.send_spi(
            &mut HvfGicEventHandler {
                wakers: &mut self.wfe_threads,
            },
            InterruptId(irq_line),
        );
    }

    fn register_vcpu(&mut self, vcpuid: u64, wfe_thread: Thread) {
        log::trace!("v3::register_vcpu({vcpuid}, {wfe_thread:?})");
        self.wfe_threads
            .insert(PeId(vcpuid), WfeThread { thread: wfe_thread });
    }

    fn vcpu_should_wait(&mut self, vcpuid: u64) -> bool {
        let vcpuid = PeId(vcpuid);
        let can_park = !self.gic.is_irq_line_asserted(
            &mut HvfGicEventHandler {
                wakers: &mut self.wfe_threads,
            },
            vcpuid,
        );

        can_park
    }

    fn vcpu_has_pending_irq(&mut self, vcpuid: u64) -> bool {
        self.gic.is_irq_line_asserted(
            &mut HvfGicEventHandler {
                wakers: &mut self.wfe_threads,
            },
            PeId(vcpuid),
        )
    }

    fn kick_vcpu(&mut self, vcpuid: u64) {
        // force exit not needed for non-IRQ kick
        let waker = self.wfe_threads.get(&PeId(vcpuid)).unwrap();
        waker.thread.unpark();
    }
}

struct HvfGicEventHandler<'a> {
    wakers: &'a mut HashMap<PeId, WfeThread>,
}

impl GicV3EventHandler for HvfGicEventHandler<'_> {
    fn kick_vcpu_for_irq(&mut self, pe: PeId) {
        let waker = self.wakers.get(&pe).unwrap();
        waker.thread.unpark();

        hvf::vcpu_request_exit(pe.0).unwrap();
    }

    // https://developer.arm.com/documentation/ddi0595/2021-12/AArch64-Registers/MPIDR-EL1--Multiprocessor-Affinity-Register
    fn get_affinity(&mut self, pe: PeId) -> Affinity {
        let mpidr = BitPack(hvf::vcpu_read_mpidr(pe.0).unwrap());
        let aff3 = mpidr.get_range(32, 39);
        let aff2 = mpidr.get_range(16, 23);
        let aff1 = mpidr.get_range(8, 15);
        let aff0 = mpidr.get_range(0, 7);

        Affinity([aff0 as u8, aff1 as u8, aff2 as u8, aff3 as u8])
    }

    fn handle_custom_eoi(&mut self, pe: PeId, int_id: InterruptId) {
        if int_id == TIMER_INT_ID {
            hvf::vcpu_set_vtimer_mask(pe.0, false).unwrap();
        }
    }
}
