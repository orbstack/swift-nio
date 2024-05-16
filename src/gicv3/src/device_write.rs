#![allow(unused_variables)]

use counter::RateCounter;

use crate::{
    device::{
        Affinity, GicV3, GicV3EventHandler, InterruptId, InterruptKind, InterruptTrigger, PeId,
    },
    mmio::{GicFullMap, GicSysReg, GicdCtlr, GICD, GICR, SGI},
    mmio_range,
    mmio_util::{
        convert_to_pod, iter_set_bits, read_bit_array, read_set_bits, BitPack, MmioWriteRequest,
    },
};

counter::counter! {
    COUNT_EOI in "gic.eoi": RateCounter = RateCounter::new(FILTER);

    COUNT_IPI_BROADCAST in "gic.ipi.broadcast": RateCounter = RateCounter::new(FILTER);
    COUNT_IPI_SINGLE in "gic.ipi.single": RateCounter = RateCounter::new(FILTER);
}

impl GicV3 {
    pub fn write_sysreg(
        &mut self,
        handler: &mut impl GicV3EventHandler,
        pe: PeId,
        reg: GicSysReg,
        value: u64,
    ) {
        tracing::trace!("--- Write GIC sysreg, PE: {pe:?}, REG: {reg:?}, VAL: {value:b}",);

        match reg {
            GicSysReg::ICC_AP0R0_EL1 => todo!(),
            GicSysReg::ICC_AP0R1_EL1 => todo!(),
            GicSysReg::ICC_AP0R2_EL1 => todo!(),
            GicSysReg::ICC_AP0R3_EL1 => todo!(),

            // 12.2.2 ICC_AP1R<n>_EL1, Interrupt Controller Active Priorities Group 1 Registers, n = 0 - 3
            GicSysReg::ICC_AP1R0_EL1 => {
                // Hardcoded both by spec and Linux.
                assert_eq!(0, value);
            }

            GicSysReg::ICC_AP1R1_EL1 => todo!(),
            GicSysReg::ICC_AP1R2_EL1 => todo!(),
            GicSysReg::ICC_AP1R3_EL1 => todo!(),
            GicSysReg::ICC_ASGI1R_EL1 => todo!(),
            GicSysReg::ICC_BPR0_EL1 => todo!(),

            // 12.2.5 ICC_BPR1_EL1, Interrupt Controller Binary Point Register 1
            GicSysReg::ICC_BPR1_EL1 => {
                // This is hardcoded in Linux.
                assert_eq!(value, 0);
            }

            // 12.2.6 ICC_CTLR_EL1, Interrupt Controller Control Register (EL1)
            GicSysReg::ICC_CTLR_EL1 => {
                // Whether this is enabled depends on `supports_deactivate_key`, which is disabled
                // if HYP support is disabled, which it is.
                assert_eq!(value, 0);
            }

            GicSysReg::ICC_DIR_EL1 => todo!(),
            GicSysReg::ICC_EOIR0_EL1 => todo!(),

            // 12.2.10 ICC_EOIR1_EL1, Interrupt Controller End Of Interrupt Register 1
            GicSysReg::ICC_EOIR1_EL1 => {
                let int_id = InterruptId(BitPack(value).get_range(0, 23) as u32);
                let pe_state = self.pe_state(handler, pe);
                let mut pe_int_state = pe_state.int_state.lock().unwrap();
                assert_eq!(pe_int_state.active_interrupt, Some(int_id));

                pe_int_state.active_interrupt = None;
                handler.handle_custom_eoi(pe, int_id);

                COUNT_EOI.count();
            }

            GicSysReg::ICC_HPPIR0_EL1 => todo!(),
            GicSysReg::ICC_HPPIR1_EL1 => todo!(),
            GicSysReg::ICC_IAR0_EL1 => todo!(),
            GicSysReg::ICC_IAR1_EL1 => todo!(),
            GicSysReg::ICC_IGRPEN0_EL1 => todo!(),

            // 12.2.16 ICC_IGRPEN1_EL1, Interrupt Controller Interrupt Group 1 Enable register
            GicSysReg::ICC_IGRPEN1_EL1 => {
                self.pe_state(handler, pe).is_enabled = BitPack(value).get_bit(0);
            }

            // 12.2.19 ICC_PMR_EL1, Interrupt Controller Interrupt Priority Mask Register
            GicSysReg::ICC_PMR_EL1 => {
                let mut priority = BitPack(value).get_range(0, 7);
                assert_eq!(value, priority); // All other bits should be reserved.

                // This replicates a quirk relied upon by `gic_has_group0` where really high
                // priorities are disregarded.
                if priority == BitPack::one_hot((8 - super::device::PRIORITY_BITS) as usize) {
                    priority = 0;
                }

                // N.B. This only affects future interrupts.
                self.pe_state(handler, pe).min_priority = priority as u8;
            }

            GicSysReg::ICC_RPR_EL1 => todo!(),
            GicSysReg::ICC_SGI0R_EL1 => todo!(),

            // 12.2.22 ICC_SGI1R_EL1, Interrupt Controller Software Generated Interrupt Group 1 Register
            GicSysReg::ICC_SGI1R_EL1 => {
                let value = BitPack(value);

                // RS, bits [47:44]
                // When ICC_CTLR_EL1.RSS==0, RS is RES0.
                assert_eq!(value.get_range(44, 47), 0);

                // IRM, bit [40]
                // Interrupt Routing Mode. Determines how the generated interrupts are distributed to
                // PEs. Possible values are:
                // 0b0 Interrupts routed to the PEs specified by Aff3.Aff2.Aff1.<target list>.
                // 0b1 Interrupts routed to all PEs in the system, excluding "self".
                let irm = value.get_bit(40);

                // INTID, bits [27:24]
                // The INTID of the SGI.
                let int_id = InterruptId(value.get_range(24, 27) as u32);
                assert_eq!(InterruptKind::SoftwareGenerated, int_id.kind());

                // Aff3, bits [55:48]
                // The affinity 3 value of the affinity path of the cluster for which SGI interrupts
                // will be generated. If the IRM bit is 1, this field is RES0.
                let aff3 = value.get_range(48, 55);
                assert!(!irm || aff3 == 0);

                // Aff2, bits [39:32]
                // The affinity 2 value of the affinity path of the cluster for which SGI interrupts
                // will be generated. If the IRM bit is 1, this field is RES0.
                let aff2 = value.get_range(32, 39);
                assert!(!irm || aff2 == 0);

                // Aff1, bits [23:16]
                // The affinity 1 value of the affinity path of the cluster for which SGI interrupts
                // will be generated. If the IRM bit is 1, this field is RES0
                let aff1 = value.get_range(16, 23);
                assert!(!irm || aff1 == 0);

                // TargetList, bits [15:0]
                // Target List. The set of PEs for which SGI interrupts will be generated. Each bit
                // corresponds to the PE within a cluster with an Affinity 0 value equal to the bit
                // number. If the IRM bit is 1, this field is RES0.
                let target_list_bits = value.get_range(0, 15);
                assert!(!irm || target_list_bits == 0);

                tracing::trace!(
                    "Generated SGI with IntId {int_id:?} targeting {aff3}.{aff2}.{aff1}.[{target_list_bits:b}], \
                     IRM={irm:?}"
                );

                // Now that the request is parsed, let's begin the interrupt procedure according
                // to section 4.1 of the spec.
                if irm {
                    for (target_pe, pe_state) in &mut self.pe_states {
                        if pe != *target_pe {
                            Self::send_interrupt_inner(handler, pe, pe_state, int_id, true);
                        }
                    }

                    COUNT_IPI_BROADCAST.count();
                } else {
                    for aff0 in iter_set_bits(target_list_bits) {
                        let target_aff = Affinity([aff0 as u8, aff1 as u8, aff2 as u8, aff3 as u8]);
                        let target_pe = self.affinity_to_pe(target_aff).unwrap();
                        let pe_state = self.pe_states.get_mut(&target_pe).unwrap();

                        Self::send_interrupt_inner(handler, target_pe, pe_state, int_id, true);
                    }

                    COUNT_IPI_SINGLE.count();
                }
            }

            GicSysReg::ICC_SRE_EL1 => todo!(),
        }
    }

    pub fn write(&mut self, pe: PeId, mut req: MmioWriteRequest<'_>) {
        tracing::trace!("--- Write GIC MMIO, PE: {pe:?}, MEM: {:?}", req.req_range(),);

        req.handle_sub(mmio_range!(GicFullMap, gicd), |req| {
            self.write_distributor(pe, req)
        });

        req.handle_sub(mmio_range!(GicFullMap, gicr), |req| {
            self.write_redistributor_rd_base(pe, req)
        });

        req.handle_sub(mmio_range!(GicFullMap, sgi), |req| {
            self.write_redistributor_sgi_base(pe, req)
        });
    }

    pub fn write_distributor(&mut self, pe: PeId, mut req: MmioWriteRequest<'_>) {
        // Handle `GICD_PIDR2` (see section 12.1.13 of spec)
        req.handle_sub(mmio_range!(GICD, id_registers), |req| {
            todo!();
        });

        // Handle `GICD_CLRSPI_NSR` (see section 12.9.1 of spec)
        req.handle_pod(mmio_range!(GICD, clrspi_nsr), |val| {
            todo!();
        });

        // Handle `GICD_CLRSPI_SR` (see section 12.9.2 of spec)
        req.handle_pod(mmio_range!(GICD, clrspi_sr), |val| {
            todo!();
        });

        // Handle `GICD_CPENDSGIR<n>` (see section 12.9.3 of spec)
        req.handle_pod_array(mmio_range!(GICD, cpendsgir), |idx, val| {
            todo!();
        });

        // Handle `GICD_CTLR` (see section 12.9.4 of spec)
        req.handle_flags(mmio_range!(GICD, ctlr), |val| {
            tracing::trace!("writing to `GICD_CTLR`");

            // This is all write-ignore:
            //
            // Register Write Pending: Read only.
            //
            // Enable 1 of N Wakeup Functionality: It is IMPLEMENTATION DEFINED whether this bit is
            // programmable, or RAZ/WI.
            //
            // Disable Security: This field is RAO/WI.
            //

            //
            // The following actually need to be implemented:
            //
            // ARE, bit [4]: Affinity Routing Enable
            //
            // - 0b0 Affinity routing disabled.
            // - 0b1 Affinity routing enabled.
            // - On a GIC reset, this field resets to 0
            //
            // N.B. Linux calls this flag `GICD_CTLR_ARE_NS` but it's actually `ARE_S`.
            // Maybe the mmio bindings are incorrect?
            self.enable_are = val.intersects(GicdCtlr::ARE_S);

            // EnableGrp1, bit [1]: Enable Group 1 interrupts.
            //
            // - 0b0 Group 1 interrupts disabled.
            // - 0b1 Group 1 interrupts enabled.
            // - On a GIC reset, this field resets to an architecturally UNKNOWN value.
            //
            self.enable_grp_1 = val.intersects(GicdCtlr::EnableGrp1NS);

            // EnableGrp0, bit [0]: Enable Group 0 interrupts.
            //
            // - 0b0 Group 0 interrupts are disabled.
            // - 0b1 Group 0 interrupts are enabled.
            // - On a GIC reset, this field resets to an architecturally UNKNOWN value.
            //
            self.enable_grp_0 = val.intersects(GicdCtlr::EnableGrp0);
        });

        // Handle `GICD_ICACTIVER<n>` (see section 12.9.5 of spec)
        req.handle_pod_array(mmio_range!(GICD, icactiver), |idx, val| {
            tracing::trace!("writing to `GICD_ICACTIVER`");

            for idx in read_set_bits(idx, val) {
                // Only "write true" has an effect.
                self.interrupt_config(pe, InterruptId(idx)).disabled = true;
            }
        });

        // Handle `GICD_ICACTIVER<n>E` (see section 12.9.6 of spec)
        req.handle_pod_array(mmio_range!(GICD, icactive_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ICENABLER<n>` (see section 12.9.7 of spec)
        req.handle_pod_array(mmio_range!(GICD, icenabler), |idx, val| {
            tracing::trace!("writing to `GICD_ICENABLER`");

            for idx in read_set_bits(idx, val) {
                // For SPIs and PPIs, controls the forwarding of interrupt number 32n + x to the CPU interfaces.
                // 0b0 If written, has no effect.
                // 0b1 If written, disables forwarding of the corresponding interrupt.
                self.interrupt_config(pe, InterruptId(idx)).not_forwarded = true;
            }
        });

        // Handle `GICD_ICENABLER<n>E` (see section 12.9.8 of spec)
        req.handle_pod_array(mmio_range!(GICD, icenabler_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ICFGR<n>` (see section 12.9.9 of spec)
        req.handle_pod_array(mmio_range!(GICD, icfgr), |idx, val| {
            tracing::trace!("writing to `GICD_ICFGR` (val: {val:b})");

            for (idx, val) in read_bit_array(idx, val, 2) {
                let trigger = InterruptTrigger::from_two_bit_repr(val);
                let iid = InterruptId(idx);

                if iid.kind() != InterruptKind::SoftwareGenerated {
                    self.interrupt_config(pe, iid).trigger = trigger;
                }
            }
        });

        // Handle `GICD_ICFGR<n>E` (see section 12.9.10 of spec)
        req.handle_pod_array(mmio_range!(GICD, icfgr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ICPENDR<n>` (see section 12.9.11 of spec)
        req.handle_pod_array(mmio_range!(GICD, icpendr), |idx, val| {
            todo!();
        });

        // Handle `GICD_ICPENDR<n>E` (see section 12.9.12 of spec)
        req.handle_pod_array(mmio_range!(GICD, icpendr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_IGROUPR<n>` (see section 12.9.13 of spec)
        req.handle_pod_array(mmio_range!(GICD, igroupr), |idx, val| {
            tracing::trace!("writing to `igroupr`");
            assert_eq!(val, u32::MAX);
        });

        // Handle `GICD_IGROUPR<n>E` (see section 12.9.14 of spec)
        req.handle_pod_array(mmio_range!(GICD, igroupr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_IGRPMODR<n>` (see section 12.9.15 of spec)
        req.handle_pod_array(mmio_range!(GICD, igrpmodr), |idx, val| {
            todo!();
        });

        // Handle `GICD_IGRPMODR<n>E` (see section 12.9.16 of spec)
        req.handle_pod_array(mmio_range!(GICD, igrpmodr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_IIDR` (see section 12.9.17 of spec)
        req.handle_pod(mmio_range!(GICD, iidr), |val| {
            todo!();
        });

        // Handle `GICD_INMIR<n>` (see section 12.9.18 of spec)
        req.handle_pod_array(mmio_range!(GICD, inmir), |idx, val| {
            todo!();
        });

        // Handle `GICD_INMIR<n>_E` (see section 12.9.19 of spec)
        // FIXME: What is this?

        // Handle `GICD_IPRIORITYR<n>` (see section 12.9.20 of spec)
        req.handle_pod_array(mmio_range!(GICD, ipriorityr), |_idx, val| {
            tracing::trace!("writing to `GICD_IPRIORITYR`");
            assert_eq!(0xa0, val);
        });

        // Handle `GICD_IPRIORITYR<n>E` (see section 12.9.21 of spec)
        req.handle_pod_array(mmio_range!(GICD, ipriorityr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_IROUTER<n>` (see section 12.9.22 of spec)
        req.handle_pod_array(mmio_range!(GICD, irouter), |idx, val| {
            tracing::trace!("writing to `GICD_IROUTER`");
            let val = BitPack(convert_to_pod::<u64>(&val));

            // This only handles SPIs so the first 32 SGI and PPI IntIds are ignored.
            let iid = InterruptId(idx as u32 + 32);
            assert_eq!(iid.kind(), InterruptKind::SharedPeripheral);

            // Ignore: Interrupt_Routing_Mode, bit [31].
            // "In implementations that do not require 1 of N distribution of SPIs, this bit
            // might be RAZ/WI"

            // Read affinity.
            let aff3 = val.get_range(32, 39);
            let aff2 = val.get_range(16, 23);
            let aff1 = val.get_range(8, 15);
            let aff0 = val.get_range(0, 7);
            self.interrupt_config(pe, iid).affinity =
                Affinity([aff0 as u8, aff1 as u8, aff2 as u8, aff3 as u8]);
        });

        // Handle `GICD_IROUTER<n>E` (see section 12.9.23 of spec)
        req.handle_pod_array(mmio_range!(GICD, irouter_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ISACTIVER<n>` (see section 12.9.24 of spec)
        req.handle_pod_array(mmio_range!(GICD, isactiver), |idx, val| {
            todo!();
        });

        // Handle `GICD_ISACTIVER<n>E` (see section 12.9.25 of spec)
        req.handle_pod_array(mmio_range!(GICD, isactive_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ISENABLER<n>` (see section 12.9.26 of spec)
        req.handle_pod_array(mmio_range!(GICD, isenabler), |idx, val| {
            tracing::trace!("writing to `GICD_ISENABLER`");

            for idx in read_set_bits(idx, val) {
                // 0b1 If read, indicates that forwarding of the corresponding interrupt is enabled.
                // If written, enables forwarding of the corresponding interrupt.
                // After a write of 1 to this bit, a subsequent read of this bit returns 1.
                self.interrupt_config(pe, InterruptId(idx)).not_forwarded = false;
            }
        });

        // Handle `GICD_ISENABLER<n>E` (see section 12.9.27 of spec)
        req.handle_pod_array(mmio_range!(GICD, isenabler_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ISPENDR<n>` (see section 12.9.28 of spec)
        req.handle_pod_array(mmio_range!(GICD, ispendr), |idx, val| {
            todo!();
        });

        // Handle `GICD_ISPENDR<n>E` (see section 12.9.29 of spec)
        req.handle_pod_array(mmio_range!(GICD, ispendr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_ITARGETSR<n>` (see section 12.9.30 of spec)
        req.handle_pod_array(mmio_range!(GICD, itargetsr), |idx, val| {
            todo!();
        });

        // Handle `GICD_NSACR<n>` (see section 12.9.31 of spec)
        req.handle_pod_array(mmio_range!(GICD, nsacr), |idx, val| {
            todo!();
        });

        // Handle `GICD_NSACR<n>E` (see section 12.9.32 of spec)
        req.handle_pod_array(mmio_range!(GICD, nsacr_e), |idx, val| {
            todo!();
        });

        // Handle `GICD_SETSPI_NSR` (see section 12.9.33 of spec)
        req.handle_pod(mmio_range!(GICD, setspi_nsr), |val| {
            todo!();
        });

        // Handle `GICD_SETSPI_SR` (see section 12.9.34 of spec)
        req.handle_pod(mmio_range!(GICD, setspi_sr), |val| {
            todo!();
        });

        // Handle `GICD_SGIR` (see section 12.9.35 of spec)
        req.handle_pod(mmio_range!(GICD, sigr), |val| {
            todo!();
        });

        // Handle `GICD_SPENDSGIR<n>` (see section 12.9.36 of spec)
        req.handle_pod_array(mmio_range!(GICD, spendsgir), |idx, val| {
            todo!();
        });

        // Handle `GICD_STATUSR` (see section 12.9.37 of spec)
        req.handle_pod(mmio_range!(GICD, statusr), |val| {
            todo!();
        });

        // Handle `GICD_TYPER` (see section 12.9.38 of spec)
        req.handle_pod(mmio_range!(GICD, typer), |val| {
            todo!();
        });

        // Handle `GICD_TYPER2` (see section 12.9.39 of spec)
        req.handle_pod(mmio_range!(GICD, typer2), |val| {
            todo!();
        });

        // Handle `GICM_CLRSPI_NSR` (see section 12.9.40 of spec)
        // FIXME: What is this?

        // Handle `GICM_CLRSPI_SR` (see section 12.9.41 of spec)
        // FIXME: What is this?

        // Handle `GICM_IIDR` (see section 12.9.42 of spec)
        // FIXME: What is this?

        // Handle `GICM_SETSPI_NSR` (see section 12.9.43 of spec)
        // FIXME: What is this?

        // Handle `GICM_SETSPI_SR` (see section 12.9.44 of spec)
        // FIXME: What is this?

        // Handle `GICM_TYPER` (see section 12.9.45 of spec)
        // FIXME: What is this?
    }

    pub fn write_redistributor_rd_base(&mut self, pe: PeId, mut req: MmioWriteRequest<'_>) {
        // Handle `GICD_PIDR2` (see section 12.1.13 of spec)
        req.handle_sub(mmio_range!(GICD, id_registers), |req| {
            todo!();
        });

        // Handle `GICR_CLRLPIR` (see section 12.11.1 of spec)
        req.handle_pod(mmio_range!(GICR, clrlpir), |val| {
            todo!();
        });

        // Handle `GICR_CTLR` (see section 12.11.2 of spec)
        req.handle_pod(mmio_range!(GICR, ctlr), |val| {
            todo!();
        });

        // Handle `GICR_IIDR` (see section 12.11.16 of spec)
        req.handle_pod(mmio_range!(GICR, iidr), |val| {
            todo!();
        });

        // Handle `GICR_INVALLR` (see section 12.11.19 of spec)
        req.handle_pod(mmio_range!(GICR, invallr), |val| {
            todo!();
        });

        // Handle `GICR_INVLPIR` (see section 12.11.20 of spec)
        req.handle_pod(mmio_range!(GICR, invlpir), |val| {
            todo!();
        });

        // Handle `GICR_MPAMIDR` (see section 12.11.29 of spec)
        req.handle_pod(mmio_range!(GICR, mpamidr), |val| {
            todo!();
        });

        // Handle `GICR_PARTIDR` (see section 12.11.31 of spec)
        req.handle_pod(mmio_range!(GICR, partidr), |val| {
            todo!();
        });

        // Handle `GICR_PENDBASER` (see section 12.11.32 of spec)
        req.handle_pod(mmio_range!(GICR, pendbaser), |val| {
            todo!();
        });

        // Handle `GICR_PROPBASER` (see section 12.11.33 of spec)
        req.handle_pod(mmio_range!(GICR, propbaser), |val| {
            todo!();
        });

        // Handle `GICR_SETLPIR` (see section 12.11.34 of spec)
        req.handle_pod(mmio_range!(GICR, setlprir), |val| {
            todo!();
        });

        // Handle `GICR_STATUSR` (see section 12.11.35 of spec)
        req.handle_pod(mmio_range!(GICR, statusr), |val| {
            todo!();
        });

        // Handle `GICR_SYNCR` (see section 12.11.36 of spec)
        req.handle_pod(mmio_range!(GICR, syncr), |val| {
            todo!();
        });

        // Handle `GICR_TYPER` (see section 12.11.37 of spec)
        req.handle_pod(mmio_range!(GICR, typer), |val| {
            todo!();
        });

        // Handle `GICR_VPENDBASER` (see section 12.11.38 of spec)
        // FIXME: What is this?

        // Handle `GICR_VPROPBASER` (see section 12.11.39 of spec)
        // FIXME: What is this?

        // Handle `GICR_VSGIPENDR` (see section 12.11.40 of spec)
        // FIXME: What is this?

        // Handle `GICR_VSGIR` (see section 12.11.41 of spec)
        // FIXME: What is this?

        // Handle `GICR_WAKER` (see section 12.11.42 of spec)
        req.handle_flags(mmio_range!(GICR, waker), |val| {
            tracing::trace!("writing to `GICR_WAKER`");
            // I don't think we have to handle sleep requests so we can just return the empty bit-set
            // to indicate that the machine is ready.
            //
            // FIXME: I'm not convinced that this is true for machine shutdown?
        });
    }

    pub fn write_redistributor_sgi_base(&mut self, pe: PeId, mut req: MmioWriteRequest<'_>) {
        // Handle `GICR_ICACTIVER0` (see section 12.11.3 of spec)
        req.handle_pod(mmio_range!(SGI, icactiver0), |val| {
            tracing::trace!("writing to `GICR_ICACTIVER0`");

            for idx in read_set_bits(0, val) {
                let iid = InterruptId(idx);

                assert!(matches!(
                    iid.kind(),
                    InterruptKind::PrivatePeripheral | InterruptKind::SoftwareGenerated
                ));

                // Removes the active state from interrupt number x. Reads and writes have the
                // following behavior:
                //
                // 0b0 If written, has no effect.
                // 0b1 If written, deactivates the corresponding interrupt, if the interrupt is active.
                self.interrupt_config(pe, iid).disabled = true;
            }
        });

        // Handle `GICR_ICACTIVER<n>E` (see section 12.11.4 of spec)
        req.handle_pod(mmio_range!(SGI, icactive_e), |val| {
            todo!();
        });

        // Handle `GICR_ICENABLER0` (see section 12.11.5 of spec)
        req.handle_pod(mmio_range!(SGI, icenabler0), |val| {
            tracing::trace!("writing to `GICR_ICENABLER0`");

            for idx in read_set_bits(0, val) {
                let iid = InterruptId(idx);

                assert!(matches!(
                    iid.kind(),
                    InterruptKind::PrivatePeripheral | InterruptKind::SoftwareGenerated
                ));

                // For PPIs and SGIs, controls the forwarding of interrupt number x to the CPU
                // interfaces. Reads and writes have the following behavior:
                //
                // 0b0 If written, has no effect.
                // 0b1 If written, disables forwarding of the corresponding interrupt.
                self.interrupt_config(pe, iid).not_forwarded = true;
            }
        });

        // Handle `GICR_ICENABLER<n>E` (see section 12.11.6 of spec)
        req.handle_pod(mmio_range!(SGI, icenabler_e), |val| {
            todo!();
        });

        // Handle `GICR_ICFGR0`, `GICR_ICFGR1`, and `GICR_ICFGR<n>E` (see section 12.11.[7-9] of spec)
        req.handle_pod_array(mmio_range!(SGI, icfgr), |idx, val| {
            for (idx, val) in read_bit_array(idx, val, 2) {
                self.interrupt_config(pe, InterruptId(idx)).trigger =
                    InterruptTrigger::from_two_bit_repr(val);
            }
        });

        // Handle `GICR_ICPENDR0` (see section 12.11.10 of spec)
        req.handle_pod(mmio_range!(SGI, icpendr0), |val| {
            todo!();
        });

        // Handle `GICR_ICPENDR<n>E` (see section 12.11.11 of spec)
        req.handle_pod_array(mmio_range!(SGI, icpendr_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_IGROUPR0` (see section 12.11.12 of spec)
        req.handle_pod(mmio_range!(SGI, igroupr0), |val| {
            tracing::trace!("writing to `GICR_IGROUPR0`");

            // All intids should be in group 1.
            assert_eq!(val, u32::MAX);
        });

        // Handle `GICR_IGROUPR<n>E` (see section 12.11.13 of spec)
        req.handle_pod_array(mmio_range!(SGI, igroupr_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_IGRPMODR0` (see section 12.11.14 of spec)
        req.handle_pod(mmio_range!(SGI, igrpmodr0), |val| {
            todo!();
        });

        // Handle `GICR_IGRPMODR<n>E` (see section 12.11.15 of spec)
        req.handle_pod_array(mmio_range!(SGI, igrpmodr_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_INMIR0` (see section 12.11.17 of spec)
        req.handle_pod(mmio_range!(SGI, inmir0), |val| {
            todo!();
        });

        // Handle `GICR_INMIR<n>E` (see section 12.11.18 of spec)
        req.handle_pod_array(mmio_range!(SGI, inmir_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_IPRIORITYR<n>` (see section 12.11.21 of spec)
        req.handle_pod_array(mmio_range!(SGI, ipriorityr), |idx, val| {
            tracing::trace!("writing to `GICR_IPRIORITYR`");
            assert_eq!(0xa0, val);
        });

        // Handle `GICR_IPRIORITYR<n>E` (see section 12.11.22 of spec)
        req.handle_pod_array(mmio_range!(SGI, ipriorityr_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_ISACTIVER0` (see section 12.11.23 of spec)
        req.handle_pod(mmio_range!(SGI, isactiver0), |val| {
            todo!();
        });

        // Handle `GICR_ISACTIVER<n>E` (see section 12.11.24 of spec)
        req.handle_pod(mmio_range!(SGI, isactive_e), |val| {
            todo!();
        });

        // Handle `GICR_ISENABLER0` (see section 12.11.25 of spec)
        req.handle_pod(mmio_range!(SGI, isenabler0), |val| {
            tracing::trace!("writing to `GICR_ISENABLER0`");

            for idx in read_set_bits(0, val) {
                let iid = InterruptId(idx);

                assert!(matches!(
                    iid.kind(),
                    InterruptKind::PrivatePeripheral | InterruptKind::SoftwareGenerated
                ));

                // For PPIs and SGIs, controls the forwarding of interrupt number x to the CPU
                // interfaces. Reads and writes have the following behavior:
                //
                // 0b0 If written, has no effect.
                // 0b1 If written, enables forwarding of the corresponding interrupt.
                self.interrupt_config(pe, iid).not_forwarded = false;
            }
        });

        // Handle `GICR_ISENABLER<n>E` (see section 12.11.26 of spec)
        req.handle_pod_array(mmio_range!(SGI, isenabler_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_ISPENDR0` (see section 12.11.27 of spec)
        req.handle_pod(mmio_range!(SGI, ispendr0), |val| {
            todo!();
        });

        // Handle `GICR_ISPENDR<n>E` (see section 12.11.28 of spec)
        req.handle_pod_array(mmio_range!(SGI, ispendr_e), |idx, val| {
            todo!();
        });

        // Handle `GICR_NSACR` (see section 12.11.30 of spec)
        req.handle_pod(mmio_range!(SGI, nsacr), |val| {
            todo!();
        });
    }
}
