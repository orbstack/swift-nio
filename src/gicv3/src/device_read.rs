#![allow(unused_variables)]

use counter::RateCounter;

use crate::{
    device::{GicV3, GicV3EventHandler, InterruptId, PeId},
    mmio::{CoreLinkIdRegisters, GicFullMap, GicSysReg, GicdCtlr, Waker, GICD, GICR, SGI},
    mmio_range,
    mmio_util::{write_bit_array, BitPack, MmioReadRequest},
};

counter::counter! {
    COUNT_GIC_ACK in "gic.sys.read.ack": RateCounter = RateCounter::new(FILTER);
    COUNT_GIC_SYSREG_READ in "gic.sys.read.total": RateCounter = RateCounter::new(FILTER);
    COUNT_GIC_MMIO_READ in "gic.mmio.read.total": RateCounter = RateCounter::new(FILTER);
}

impl GicV3 {
    pub fn read_sysreg(
        &mut self,
        handler: &mut impl GicV3EventHandler,
        pe: PeId,
        reg: GicSysReg,
    ) -> u64 {
        tracing::trace!("--- Read GIC sysreg, PE: {pe:?}, REG: {:?}", reg,);

        COUNT_GIC_SYSREG_READ.count();
        match reg {
            GicSysReg::ICC_AP0R0_EL1 => todo!(),
            GicSysReg::ICC_AP0R1_EL1 => todo!(),
            GicSysReg::ICC_AP0R2_EL1 => todo!(),
            GicSysReg::ICC_AP0R3_EL1 => todo!(),
            GicSysReg::ICC_AP1R0_EL1 => todo!(),
            GicSysReg::ICC_AP1R1_EL1 => todo!(),
            GicSysReg::ICC_AP1R2_EL1 => todo!(),
            GicSysReg::ICC_AP1R3_EL1 => todo!(),
            GicSysReg::ICC_ASGI1R_EL1 => todo!(),
            GicSysReg::ICC_BPR0_EL1 => todo!(),
            GicSysReg::ICC_BPR1_EL1 => todo!(),

            // 12.2.6 ICC_CTLR_EL1, Interrupt Controller Control Register (EL1)
            GicSysReg::ICC_CTLR_EL1 => {
                BitPack::default()
                    // ExtRange, bit [19]: Extended INTID range (read-only).
                    // 0b0 CPU interface does not support INTIDs in the range 1024..8191
                    //
                    // RSS, bit [18]: Range Selector Support. This bit is read only.
                    // 0b0 Targeted SGIs with affinity level 0 values of 0 - 15 are supported
                    //
                    // A3V, bit [15]: Affinity 3 Valid. Read-only and writes are ignored.
                    // 0b0 The CPU interface logic only supports zero values of Affinity 3 in SGI generation
                    // System registers.
                    //
                    // SEIS, bit [14]: SEI Support. Read-only and writes are ignored. Indicates
                    // whether the CPU interface supports local generation of SEIs:
                    // 0b0 The CPU interface logic does not support local generation of SEIs.
                    //
                    // IDbits, bits [13:11]: Identifier bits. Read-only and writes are ignored. The
                    // number of physical interrupt identifier bits supported.
                    // 0b000 16 bits.
                    //
                    // PRIbits, bits [10:8]
                    // Priority bits. Read-only and writes are ignored. The number of priority bits
                    // implemented, minus one.
                    .set_range(8, 10, super::device::PRIORITY_BITS - 1)
                    // PMHE, bit [6]: Priority Mask Hint Enable. Controls whether the priority mask
                    // register is used as a hint for interrupt distribution.
                    // 0b0 Disables use of ICC_PMR_EL1 as a hint for interrupt distribution.
                    //
                    // EOImode, bit [1]: EOI mode for the current Security state. Controls whether a
                    // write to an End of Interrupt register also deactivates the interrupt.
                    //
                    // 0b0 ICC_EOIR0_EL1 and ICC_EOIR1_EL1 provide both priority drop and interrupt
                    // deactivation functionality. Accesses to ICC_DIR_EL1 are UNPREDICTABLE
                    //
                    // N.B. This probably affects performance quite a bit since it effectively halves
                    //  the number of vmexits to send an SGI
                    //
                    // CBPR, bit [0]: Common Binary Point Register. Controls whether the same register
                    // is used for interrupt preemption of both Group 0 and Group 1 interrupts.
                    //
                    // 0b1 ICC_BPR0_EL1 determines the preemption group for both Group 0 and Group 1
                    // interrupts.
                    .set_bit(0, true)
                    .0
            }
            GicSysReg::ICC_DIR_EL1 => todo!(),
            GicSysReg::ICC_EOIR0_EL1 => todo!(),
            GicSysReg::ICC_EOIR1_EL1 => todo!(),
            GicSysReg::ICC_HPPIR0_EL1 => todo!(),
            GicSysReg::ICC_HPPIR1_EL1 => todo!(),
            GicSysReg::ICC_IAR0_EL1 => todo!(),

            // 12.2.14 ICC_IAR1_EL1, Interrupt Controller Interrupt Acknowledge Register 1
            GicSysReg::ICC_IAR1_EL1 => {
                COUNT_GIC_ACK.count();

                let pe_state = self.pe_state(handler, pe);
                let mut pe_int_state = pe_state.int_state.lock().unwrap();

                let active_int_id = if let Some(active) = pe_int_state.active_interrupt {
                    active
                } else if let Some(front) = pe_int_state.pending_interrupts.pop_front() {
                    pe_int_state.active_interrupt = Some(front);
                    front
                } else {
                    InterruptId(1023)
                };

                BitPack::default()
                    .set_range(0, 23, active_int_id.0 as u64)
                    .0
            }

            GicSysReg::ICC_IGRPEN0_EL1 => todo!(),
            GicSysReg::ICC_IGRPEN1_EL1 => todo!(),

            // 12.2.19 ICC_PMR_EL1, Interrupt Controller Interrupt Priority Mask Register
            GicSysReg::ICC_PMR_EL1 => self.pe_state(handler, pe).min_priority as u64,
            GicSysReg::ICC_RPR_EL1 => todo!(),
            GicSysReg::ICC_SGI0R_EL1 => todo!(),
            GicSysReg::ICC_SGI1R_EL1 => todo!(),
            GicSysReg::ICC_SRE_EL1 => todo!(),
        }
    }

    pub fn read(
        &mut self,
        handler: &mut impl GicV3EventHandler,
        pe: PeId,
        mut req: MmioReadRequest<'_>,
    ) {
        tracing::trace!("--- Read GIC MMIO, PE: {pe:?}, MEM: {:?}", req.req_range(),);
        COUNT_GIC_MMIO_READ.count();

        req.handle_sub(mmio_range!(GicFullMap, gicd), |req| {
            self.read_distributor(pe, req)
        });

        req.handle_sub(mmio_range!(GicFullMap, gicr), |req| {
            self.read_redistributor_rd_base(handler, pe, req)
        });

        req.handle_sub(mmio_range!(GicFullMap, sgi), |req| {
            self.read_redistributor_sgi_base(pe, req)
        });
    }

    pub fn read_distributor(&mut self, pe: PeId, mut req: MmioReadRequest<'_>) {
        // Handle `GICD_PIDR2` (see section 12.1.13 of spec)
        req.handle_sub(mmio_range!(GICD, id_registers), |req| {
            Self::read_core_link_id(req, 3)
        });

        // Handle `GICD_CLRSPI_NSR` (see section 12.9.1 of spec)
        req.handle_pod(mmio_range!(GICD, clrspi_nsr), || {
            todo!();
        });

        // Handle `GICD_CLRSPI_SR` (see section 12.9.2 of spec)
        req.handle_pod(mmio_range!(GICD, clrspi_sr), || {
            todo!();
        });

        // Handle `GICD_CPENDSGIR<n>` (see section 12.9.3 of spec)
        req.handle_pod_array(mmio_range!(GICD, cpendsgir), |idx| {
            todo!();
        });

        // Handle `GICD_CTLR` (see section 12.9.4 of spec)
        req.handle_flags(mmio_range!(GICD, ctlr), || {
            tracing::trace!("Read `GICD_CTLR`");
            let mut flags = GicdCtlr::DS; // for GICs with one security level, this is RAO/WI.

            // bit 5, `ARE_NS`, is reserved for GICs with one security level. This is contrary
            // to the OSDev wiki, which claims that they're also RAO/WI but what do they know?
            if self.enable_are {
                flags |= GicdCtlr::ARE_S;
            }

            if self.enable_grp_1 {
                // The secure version of this flag is also reserved in this context.
                flags |= GicdCtlr::EnableGrp1NS;
            }

            if self.enable_grp_0 {
                flags |= GicdCtlr::EnableGrp0;
            }

            flags
        });

        // Handle `GICD_ICACTIVER<n>` (see section 12.9.5 of spec)
        req.handle_pod_array(mmio_range!(GICD, icactiver), |idx| {
            todo!();
        });

        // Handle `GICD_ICACTIVER<n>E` (see section 12.9.6 of spec)
        req.handle_pod_array(mmio_range!(GICD, icactive_e), |idx| {
            todo!();
        });

        // Handle `GICD_ICENABLER<n>` (see section 12.9.7 of spec)
        req.handle_pod_array(mmio_range!(GICD, icenabler), |idx| {
            todo!();
        });

        // Handle `GICD_ICENABLER<n>E` (see section 12.9.8 of spec)
        req.handle_pod_array(mmio_range!(GICD, icenabler_e), |idx| {
            todo!();
        });

        // Handle `GICD_ICFGR<n>` (see section 12.9.9 of spec)
        req.handle_pod_array(mmio_range!(GICD, icfgr), |idx| {
            tracing::trace!("reading from `GICD_ICFGR`");
            write_bit_array(idx, 2, |idx| {
                let trigger = self.interrupt_config(pe, InterruptId(idx as u32)).trigger;
                tracing::trace!("{idx} = {trigger:?}");
                trigger.to_two_bit_repr()
            })
        });

        // Handle `GICD_ICFGR<n>E` (see section 12.9.10 of spec)
        req.handle_pod_array(mmio_range!(GICD, icfgr_e), |idx| {
            todo!();
        });

        // Handle `GICD_ICPENDR<n>` (see section 12.9.11 of spec)
        req.handle_pod_array(mmio_range!(GICD, icpendr), |idx| {
            todo!();
        });

        // Handle `GICD_ICPENDR<n>E` (see section 12.9.12 of spec)
        req.handle_pod_array(mmio_range!(GICD, icpendr_e), |idx| {
            todo!();
        });

        // Handle `GICD_IGROUPR<n>` (see section 12.9.13 of spec)
        req.handle_pod_array(mmio_range!(GICD, igroupr), |idx| {
            todo!();
        });

        // Handle `GICD_IGROUPR<n>E` (see section 12.9.14 of spec)
        req.handle_pod_array(mmio_range!(GICD, igroupr_e), |idx| {
            todo!();
        });

        // Handle `GICD_IGRPMODR<n>` (see section 12.9.15 of spec)
        req.handle_pod_array(mmio_range!(GICD, igrpmodr), |idx| {
            todo!();
        });

        // Handle `GICD_IGRPMODR<n>E` (see section 12.9.16 of spec)
        req.handle_pod_array(mmio_range!(GICD, igrpmodr_e), |idx| {
            todo!();
        });

        // Handle `GICD_IIDR` (see section 12.9.17 of spec)
        req.handle_pod(mmio_range!(GICD, iidr), || {
            tracing::trace!("Read `GICD_IIDR`");

            BitPack::default()
                // ProductID, bits [31:24]: Product Identifier.
                .set_range(24, 31, 0xBADF00D)
                // Variant, bits [19:16]: Variant number. Typically, this field is used to distinguish product variants, or major revisions of a product.
                .set_range(16, 19, 0)
                // Revision, bits [15:12]: Revision number. Typically, this field is used to distinguish minor revisions of a product.
                .set_range(12, 15, 0)
                // Implementer, bits [11:0]: Contains the JEP106 code of the company that implemented the Distributor:
                .set_range(12, 15, 0)
                .0
        });

        // Handle `GICD_INMIR<n>` (see section 12.9.18 of spec)
        req.handle_pod_array(mmio_range!(GICD, inmir), |idx| {
            todo!();
        });

        // Handle `GICD_INMIR<n>_E` (see section 12.9.19 of spec)
        // FIXME: What is this?

        // Handle `GICD_IPRIORITYR<n>` (see section 12.9.20 of spec)
        req.handle_pod_array(mmio_range!(GICD, ipriorityr), |idx| {
            todo!();
        });

        // Handle `GICD_IPRIORITYR<n>E` (see section 12.9.21 of spec)
        req.handle_pod_array(mmio_range!(GICD, ipriorityr_e), |idx| {
            todo!();
        });

        // Handle `GICD_IROUTER<n>` (see section 12.9.22 of spec)
        req.handle_pod_array(mmio_range!(GICD, irouter), |idx| {
            todo!();
        });

        // Handle `GICD_IROUTER<n>E` (see section 12.9.23 of spec)
        req.handle_pod_array(mmio_range!(GICD, irouter_e), |idx| {
            todo!();
        });

        // Handle `GICD_ISACTIVER<n>` (see section 12.9.24 of spec)
        req.handle_pod_array(mmio_range!(GICD, isactiver), |idx| {
            todo!();
        });

        // Handle `GICD_ISACTIVER<n>E` (see section 12.9.25 of spec)
        req.handle_pod_array(mmio_range!(GICD, isactive_e), |idx| {
            todo!();
        });

        // Handle `GICD_ISENABLER<n>` (see section 12.9.26 of spec)
        req.handle_pod_array(mmio_range!(GICD, isenabler), |idx| {
            write_bit_array(idx, 1, |idx| {
                // 0b0 If read, indicates that forwarding of the corresponding interrupt is disabled.
                // 0b1 If read, indicates that forwarding of the corresponding interrupt is enabled.

                if self.interrupt_config(pe, InterruptId(idx as u32)).disabled {
                    0
                } else {
                    1
                }
            })
        });

        // Handle `GICD_ISENABLER<n>E` (see section 12.9.27 of spec)
        req.handle_pod_array(mmio_range!(GICD, isenabler_e), |idx| {
            todo!();
        });

        // Handle `GICD_ISPENDR<n>` (see section 12.9.28 of spec)
        req.handle_pod_array(mmio_range!(GICD, ispendr), |idx| {
            todo!();
        });

        // Handle `GICD_ISPENDR<n>E` (see section 12.9.29 of spec)
        req.handle_pod_array(mmio_range!(GICD, ispendr_e), |idx| {
            todo!();
        });

        // Handle `GICD_ITARGETSR<n>` (see section 12.9.30 of spec)
        req.handle_pod_array(mmio_range!(GICD, itargetsr), |idx| {
            todo!();
        });

        // Handle `GICD_NSACR<n>` (see section 12.9.31 of spec)
        req.handle_pod_array(mmio_range!(GICD, nsacr), |idx| {
            todo!();
        });

        // Handle `GICD_NSACR<n>E` (see section 12.9.32 of spec)
        req.handle_pod_array(mmio_range!(GICD, nsacr_e), |idx| {
            todo!();
        });

        // Handle `GICD_SETSPI_NSR` (see section 12.9.33 of spec)
        req.handle_pod(mmio_range!(GICD, setspi_nsr), || {
            todo!();
        });

        // Handle `GICD_SETSPI_SR` (see section 12.9.34 of spec)
        req.handle_pod(mmio_range!(GICD, setspi_sr), || {
            todo!();
        });

        // Handle `GICD_SGIR` (see section 12.9.35 of spec)
        req.handle_pod(mmio_range!(GICD, sigr), || {
            todo!();
        });

        // Handle `GICD_SPENDSGIR<n>` (see section 12.9.36 of spec)
        req.handle_pod_array(mmio_range!(GICD, spendsgir), |idx| {
            todo!();
        });

        // Handle `GICD_STATUSR` (see section 12.9.37 of spec)
        req.handle_pod(mmio_range!(GICD, statusr), || {
            todo!();
        });

        // Handle `GICD_TYPER` (see section 12.9.38 of spec)
        req.handle_pod(mmio_range!(GICD, typer), || {
            tracing::trace!("Read `GICD_TYPER`");

            BitPack::default()
                // RSS, bit [26]: Range Selector Support.
                // 0b1 The IRI supports targeted SGIs with affinity level 0 values of 0 - 255.
                .set_bit(26, true)
                // No1N, bit [25]: Indicates whether 1 of N SPI interrupts are supported.
                // 0b1 1 of N SPI interrupts are not supported.
                .set_bit(25, true)
                // A3V, bit [24]: Affinity 3 valid. Indicates whether the Distributor supports nonzero values of Affinity level 3.
                // 0b0 The Distributor only supports zero values of Affinity level 3.
                .set_bit(24, false)
                // IDbits, bits [23:19]: The number of interrupt identifier bits supported, minus one.
                .set_range(19, 23, InterruptId::BITS - 1)
                // LPIS, bit [17]: Indicates whether the implementation supports LPIs.
                // 0b0 The implementation does not support LPIs.
                .set_bit(17, false)
                // MBIS, bit [16]: Indicates whether the implementation supports message-based interrupts by writing to Distributor registers.
                // 0b0 The implementation does not support message-based interrupts by writing to Distributor registers.
                .set_bit(16, false)
                // num_LPIs, bits [15:11]: Number of supported LPIs.
                // 0b00000 Number of LPIs as indicated by GICD_TYPER.IDbits.
                .set_range(11, 15, 0)
                // SecurityExtn, bit [10]: Indicates whether the GIC implementation supports two Security states
                // When GICD_CTLR.DS == 1, this field is RAZ.
                // 0b0 The GIC implementation supports only a single Security state.
                .set_bit(10, false)
                // NMI, bit [9]: Non-maskable Interrupts.
                // 0b0 Non-maskable interrupt property not supported.
                .set_bit(9, false)
                // ESPI, bit [8]: Extended SPI.
                // 0b0 Extended SPI range not implemented.
                .set_bit(8, false)
                // CPUNumber, bits [7:5]: Reports the number of PEs that can be used when affinity routing is not enabled, minus 1.
                // If the implementation does not support ARE being zero, this field is 000.
                .set_range(5, 7, 0)
                // ITLinesNumber, bits [4:0]: For the INTID range 32 to 1019, indicates the maximum SPI supported.
                // If the value of this field is N, the maximum SPI INTID is 32(N+1) minus 1. For example, 00011 specifies that the maximum SPI INTID in is 127.
                .set_range(0, 4, 0b00011)
                .0
        });

        // Handle `GICD_TYPER2` (see section 12.9.39 of spec)
        req.handle_pod(mmio_range!(GICD, typer2), || {
            tracing::trace!("Read `GICD_TYPER2`");

            BitPack::default()
                // nASSGIcap, bit [8]: Indicates whether SGIs can be configured to not have an active state.
                // 0b0 SGIs have an active state.
                .set_bit(8, false)
                // VIL, bit [7]: Indicates whether 16 bits of vPEID are implemented.
                // 0b0 GIC supports 16-bit vPEID.
                .set_bit(7, false)
                // VID, bits [4:0]
                // When GICD_TYPER2.VIL == 0, this field is RES0.
                .set_range(0, 4, 0)
                .0
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

    pub fn read_redistributor_rd_base(
        &mut self,
        handler: &mut impl GicV3EventHandler,
        pe: PeId,
        mut req: MmioReadRequest<'_>,
    ) {
        // Handle `GICD_PIDR2` (see section 12.1.13 of spec)
        req.handle_sub(mmio_range!(GICD, id_registers), |req| {
            Self::read_core_link_id(req, 3)
        });

        // Handle `GICR_CLRLPIR` (see section 12.11.1 of spec)
        req.handle_pod(mmio_range!(GICR, clrlpir), || {
            todo!();
        });

        // Handle `GICR_CTLR` (see section 12.11.2 of spec)
        req.handle_pod(mmio_range!(GICR, ctlr), || {
            tracing::trace!("Read `GICR_CTLR`");
            BitPack::default()
                // UWP, bit [31]: Upstream Write Pending. Read-only. Indicates whether all upstream
                //                writes have been communicated to the Distributor
                //
                // 0b0 The effects of all upstream writes have been communicated to the Distributor,
                // including any Generate SGI packets. For more information, see Generate SGI.
                .set_bit(31, false)
                // DPG1S, bit [26]: Disable Processor selection for Group 1 Secure interrupts.
                //
                // When GICR_TYPER.DPGS == 0 this bit is RAZ/WI.
                .set_bit(26, false)
                // DPG1NS, bit [25]: Disable Processor selection for Group 1 Non-secure interrupts.
                //
                // When GICR_TYPER.DPGS == 0 this bit is RAZ/WI.
                .set_bit(25, false)
                // DPG0, bit [24]: Disable Processor selection for Group 0 interrupts.
                //
                // When GICR_TYPER.DPGS == 0 this bit is RAZ/WI.
                .set_bit(24, false)
                // RWP, bit [3]: Register Write Pending. This bit indicates whether a register write
                // for the current Security state is in progress or not.
                //
                // 0b0 The effect of all previous writes to the following registers are visible to
                // all agents in the system:
                //
                //    ...
                //
                .set_bit(3, false)
                // IR, bit [2]: LPI invaldiate registers supported. This bit is read-only.
                //
                // 0b0 This bit does not indicate whether the GICR_INVLPIR, GICR_INVALLR and
                //     GICR_SYNCR are implemented or not.
                .set_bit(2, false)
                // CES, bit [1]: Clear Enable Supported. This bit is read-only.
                //
                // 0b0 The IRI does not indicate whether GICR_CTLR.EnableLPIs is RES1 once set.
                //
                // When GICR_CLTR.CES == 0, software cannot assume that GICR_CTLR.EnableLPIs is
                // programmable without observing the bit being cleared.
                .set_bit(1, false)
                // EnableLPIs, bit [0]: In implementations where affinity routing is enabled for the
                // Security state:
                //
                // 0b0 LPI support is disabled. Any doorbell interrupt generated as a result of a
                // write to a virtual LPI register must be discarded, and any ITS translation requests
                // or commands involving LPIs in this Redistributor are ignored.
                .set_bit(0, false)
                .0
        });

        // Handle `GICR_IIDR` (see section 12.11.16 of spec)
        req.handle_pod(mmio_range!(GICR, iidr), || {
            todo!();
        });

        // Handle `GICR_INVALLR` (see section 12.11.19 of spec)
        req.handle_pod(mmio_range!(GICR, invallr), || {
            todo!();
        });

        // Handle `GICR_INVLPIR` (see section 12.11.20 of spec)
        req.handle_pod(mmio_range!(GICR, invlpir), || {
            todo!();
        });

        // Handle `GICR_MPAMIDR` (see section 12.11.29 of spec)
        req.handle_pod(mmio_range!(GICR, mpamidr), || {
            todo!();
        });

        // Handle `GICR_PARTIDR` (see section 12.11.31 of spec)
        req.handle_pod(mmio_range!(GICR, partidr), || {
            todo!();
        });

        // Handle `GICR_PENDBASER` (see section 12.11.32 of spec)
        req.handle_pod(mmio_range!(GICR, pendbaser), || {
            todo!();
        });

        // Handle `GICR_PROPBASER` (see section 12.11.33 of spec)
        req.handle_pod(mmio_range!(GICR, propbaser), || {
            todo!();
        });

        // Handle `GICR_SETLPIR` (see section 12.11.34 of spec)
        req.handle_pod(mmio_range!(GICR, setlprir), || {
            todo!();
        });

        // Handle `GICR_STATUSR` (see section 12.11.35 of spec)
        req.handle_pod(mmio_range!(GICR, statusr), || {
            todo!();
        });

        // Handle `GICR_SYNCR` (see section 12.11.36 of spec)
        req.handle_pod(mmio_range!(GICR, syncr), || {
            todo!();
        });

        // Handle `GICR_TYPER` (see section 12.11.37 of spec)
        req.handle_pod(mmio_range!(GICR, typer), || {
            tracing::trace!("Read `GICR_TYPER`");
            BitPack::default()
                // Affinity_Value, bits [63:32]: The identity of the PE associated with this Redistributor.
                .set_range(
                    32,
                    63,
                    self.pe_state(handler, pe).affinity.as_typer_id().into(),
                )
                // PPInum, bits [31:27]
                //
                // When FEAT_GICv3p1 is not implemented, RES0.
                //
                // The kernel takes this value as indicating a PPI count of 16 (Maximum PPI INTID is 31)
                .set_range(27, 31, 0b00000)
                // CommonLPIAff, bits [25:24]: The affinity level at which Redistributors share an LPI Configuration table.
                // 0b00 All Redistributors must share an LPI Configuration table.
                .set_range(24, 25, 0b00)
                // Processor_Number, bits [23:8]: A unique identifier for the PE. When GITS_TYPER.PTA == 0,
                // an ITS uses this field to identify the interrupt target.
                .set_range(8, 23, pe.0)
                // DPGS, bit [5]: Sets support for GICR_CTLR.DPG* bits.
                //
                // 0b0 GICR_CTLR.DPG* bits are not supported
                .set_bit(5, false)
                // Last, bit [4]: Indicates whether this Redistributor is the highest-numbered
                //                Redistributor in a series of contiguous Redistributor pages.
                //
                // 0b1 This Redistributor is the highest-numbered Redistributor in a series of
                //     contiguous Redistributor pages
                .set_bit(4, true)
                // DirectLPI, bit [3]: Indicates whether this Redistributor supports direct injection of LPIs.
                // 0b0 This Redistributor does not support direct injection of LPIs.
                .set_bit(3, false)
                // Dirty, bit [2]: Controls the functionality of GICR_VPENDBASER.Dirty.
                // When GICR_TYPER.VLPIS == 0, this field is RES0.
                .set_bit(2, false)
                // VLPIS, bit [1]: Indicates whether the GIC implementation supports virtual LPIs and
                //                 the direct injection of virtual LPIs.
                //
                // 0b0 The implementation does not support virtual LPIs or the direct injection of virtual LPIs.
                .set_bit(1, false)
                // PLPIS, bit [0]: Indicates whether the GIC implementation supports physical LPIs.
                //
                // 0b0 The implementation does not support physical LPIs.
                .set_bit(0, false)
                .0
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
        req.handle_flags(mmio_range!(GICR, waker), || {
            tracing::trace!("read `GICR_WAKER`");
            // I don't think we have to handle sleep requests so we can just return the empty bit-set
            // to indicate that the machine is ready.
            //
            // FIXME: I'm not convinced that this is true for machine shutdown?
            Waker::empty()
        });
    }

    pub fn read_redistributor_sgi_base(&mut self, pe: PeId, mut req: MmioReadRequest<'_>) {
        // Handle `GICR_ICACTIVER0` (see section 12.11.3 of spec)
        req.handle_pod(mmio_range!(SGI, isactiver0), || {
            todo!();
        });

        // Handle `GICR_ICACTIVER<n>E` (see section 12.11.4 of spec)
        req.handle_pod(mmio_range!(SGI, isactive_e), || {
            todo!();
        });

        // Handle `GICR_ICENABLER0` (see section 12.11.5 of spec)
        req.handle_pod(mmio_range!(SGI, icenabler0), || {
            todo!();
        });

        // Handle `GICR_ICENABLER<n>E` (see section 12.11.6 of spec)
        req.handle_pod(mmio_range!(SGI, icenabler_e), || {
            todo!();
        });

        // Handle `GICR_ICFGR0`, `GICR_ICFGR1`, and `GICR_ICFGR<n>E` (see section 12.11.[7-9] of spec)
        req.handle_pod_array(mmio_range!(SGI, icfgr), |idx| {
            tracing::trace!("read `GICR_ICFGR<n>`");
            assert!(idx <= 1, "extended LPI range is not supported");

            write_bit_array(idx, 2, |idx| {
                self.interrupt_config(pe, InterruptId(idx as u32))
                    .trigger
                    .to_two_bit_repr()
            })
        });

        // Handle `GICR_ICPENDR0` (see section 12.11.10 of spec)
        req.handle_pod(mmio_range!(SGI, icpendr0), || {
            todo!();
        });

        // Handle `GICR_ICPENDR<n>E` (see section 12.11.11 of spec)
        req.handle_pod_array(mmio_range!(SGI, icpendr_e), |idx| {
            todo!();
        });

        // Handle `GICR_IGROUPR0` (see section 12.11.12 of spec)
        req.handle_pod(mmio_range!(SGI, igroupr0), || {
            todo!();
        });

        // Handle `GICR_IGROUPR<n>E` (see section 12.11.13 of spec)
        req.handle_pod_array(mmio_range!(SGI, igroupr_e), |idx| {
            todo!();
        });

        // Handle `GICR_IGRPMODR0` (see section 12.11.14 of spec)
        req.handle_pod(mmio_range!(SGI, igrpmodr0), || {
            todo!();
        });

        // Handle `GICR_IGRPMODR<n>E` (see section 12.11.15 of spec)
        req.handle_pod_array(mmio_range!(SGI, igrpmodr_e), |idx| {
            todo!();
        });

        // Handle `GICR_INMIR0` (see section 12.11.17 of spec)
        req.handle_pod(mmio_range!(SGI, inmir0), || {
            todo!();
        });

        // Handle `GICR_INMIR<n>E` (see section 12.11.18 of spec)
        req.handle_pod_array(mmio_range!(SGI, inmir_e), |idx| {
            todo!();
        });

        // Handle `GICR_IPRIORITYR<n>` (see section 12.11.21 of spec)
        req.handle_pod_array(mmio_range!(SGI, ipriorityr), |idx| {
            todo!();
        });

        // Handle `GICR_IPRIORITYR<n>E` (see section 12.11.22 of spec)
        req.handle_pod_array(mmio_range!(SGI, ipriorityr_e), |idx| {
            todo!();
        });

        // Handle `GICR_ISACTIVER0` (see section 12.11.23 of spec)
        req.handle_pod(mmio_range!(SGI, isactiver0), || {
            todo!();
        });

        // Handle `GICR_ISACTIVER<n>E` (see section 12.11.24 of spec)
        req.handle_pod(mmio_range!(SGI, isactive_e), || {
            todo!();
        });

        // Handle `GICR_ISENABLER0` (see section 12.11.25 of spec)
        req.handle_pod(mmio_range!(SGI, isenabler0), || {
            todo!();
        });

        // Handle `GICR_ISENABLER<n>E` (see section 12.11.26 of spec)
        req.handle_pod_array(mmio_range!(SGI, isenabler_e), |idx| {
            todo!();
        });

        // Handle `GICR_ISPENDR0` (see section 12.11.27 of spec)
        req.handle_pod(mmio_range!(SGI, ispendr0), || {
            todo!();
        });

        // Handle `GICR_ISPENDR<n>E` (see section 12.11.28 of spec)
        req.handle_pod_array(mmio_range!(SGI, ispendr_e), |idx| {
            todo!();
        });

        // Handle `GICR_NSACR` (see section 12.11.30 of spec)
        req.handle_pod(mmio_range!(SGI, nsacr), || {
            todo!();
        });
    }

    pub fn read_core_link_id(mut req: MmioReadRequest<'_>, version: u32) {
        req.handle_pod(mmio_range!(CoreLinkIdRegisters, pidr2), || {
            tracing::trace!("Read `PIDR2`");
            BitPack::default()
                // ArchRev
                .set_range(4, 7, version)
                .0
        });
    }
}
