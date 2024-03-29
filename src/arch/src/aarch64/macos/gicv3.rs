// Copyright 2019 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

//  References:
//  - https://www.kernel.org/doc/Documentation/devicetree/bindings/interrupt-controller/arm%2Cgic-v3.txt
//  - https://docs.kernel.org/devicetree/usage-model.html

use std::{boxed::Box, mem::size_of, result};

use gicv3::{device::InterruptId, mmio::GicFullMap, mmio_range, mmio_util::MmioRange};

use super::gic::{Error, GICDevice};

type Result<T> = result::Result<T, Error>;

// === Layout === //

/// This is just a placeholder for building the FDT entry.
/// The actual emulated GICv3 is in devices/legacy.
pub struct GICv3 {
    vcpu_count: u64,
    reg_props: [u64; 4],
}

impl GICv3 {
    pub fn mapped_range() -> MmioRange {
        MmioRange::new(
            super::super::layout::MAPPED_IO_START - size_of::<GicFullMap>() as u64,
            super::super::layout::MAPPED_IO_START,
        )
    }

    pub fn gicd_range() -> MmioRange {
        mmio_range!(GicFullMap, gicd)
            .raw()
            .offset(Self::mapped_range().start)
    }

    pub fn gicr_range() -> MmioRange {
        mmio_range!(GicFullMap, gicr)
            .raw()
            .union(mmio_range!(GicFullMap, sgi).raw())
            .offset(Self::mapped_range().start)
    }

    pub fn vgic_maintenance_id() -> InterruptId {
        InterruptId(8)
        // InterruptId(9)
    }
}

impl GICDevice for GICv3 {
    fn version() -> u32 {
        0
    }

    fn device_properties(&self) -> &[u64] {
        &self.reg_props
    }

    fn vcpu_count(&self) -> u64 {
        self.vcpu_count
    }

    fn fdt_compatibility(&self) -> &str {
        // - compatible : should at least contain "arm,gic-v3" or either "qcom,msm8996-gic-v3",
        // "arm,gic-v3" for msm8996 SoCs to address SoC specific bugs/quirks
        "arm,gic-v3"
    }

    fn fdt_maint_irq(&self) -> u32 {
        // - interrupts : Interrupt source of the VGIC maintenance interrupt.
        Self::vgic_maintenance_id().0
    }

    fn create_device(vcpu_count: u64) -> Box<dyn GICDevice> {
        Box::new(GICv3 {
            vcpu_count, // reg : Specifies base physical address(s) and size of the GIC registers, in the following order:
            reg_props: [
                // - GIC Distributor interface (GICD)
                Self::gicd_range().start,
                Self::gicd_range().size(),
                // - GIC Redistributors (GICR), one range per redistributor region
                Self::gicr_range().start,
                Self::gicr_range().size(),
                // GICC, GICH and GICV are optional.
                // - GIC CPU interface (GICC)
                // - GIC Hypervisor interface (GICH)
                // - GIC Virtual CPU interface (GICV)
            ],
        })
    }

    fn init_device_attributes(_gic_device: &Box<dyn GICDevice>) -> Result<()> {
        Ok(())
    }
}
