// Copyright 2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Portions Copyright 2017 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the THIRD-PARTY file.

// For GDT details see arch/x86/include/asm/segment.h

#[cfg(target_os = "linux")]
use kvm_bindings::kvm_segment;

#[cfg(not(target_os = "linux"))]
#[derive(Debug, Default, Copy, Clone, PartialEq)]
#[cfg_attr(
    feature = "serde",
    derive(zerocopy::AsBytes, zerocopy::FromBytes, zerocopy::FromZeroes)
)]
#[allow(non_camel_case_types)]
pub struct kvm_segment {
    pub base: u64,
    pub limit: u32,
    pub selector: u16,
    pub type_: u8,
    pub present: u8,
    pub dpl: u8,
    pub db: u8,
    pub s: u8,
    pub l: u8,
    pub g: u8,
    pub avl: u8,
    pub unusable: u8,
    pub padding: u8,
}

/// Constructor for a conventional segment GDT (or LDT) entry. Derived from the kernel's segment.h.
pub fn gdt_entry(flags: u16, base: u32, limit: u32) -> u64 {
    ((u64::from(base) & 0xff00_0000u64) << (56 - 24))
        | ((u64::from(flags) & 0x0000_f0ffu64) << 40)
        | ((u64::from(limit) & 0x000f_0000u64) << (48 - 16))
        | ((u64::from(base) & 0x00ff_ffffu64) << 16)
        | (u64::from(limit) & 0x0000_ffffu64)
}

fn get_base(entry: u64) -> u64 {
    (((entry) & 0xFF00_0000_0000_0000) >> 32)
        | (((entry) & 0x0000_00FF_0000_0000) >> 16)
        | (((entry) & 0x0000_0000_FFFF_0000) >> 16)
}

fn get_limit(entry: u64) -> u32 {
    ((((entry) & 0x000F_0000_0000_0000) >> 32) | ((entry) & 0x0000_0000_0000_FFFF)) as u32
}

fn get_g(entry: u64) -> u8 {
    ((entry & 0x0080_0000_0000_0000) >> 55) as u8
}

fn get_db(entry: u64) -> u8 {
    ((entry & 0x0040_0000_0000_0000) >> 54) as u8
}

fn get_l(entry: u64) -> u8 {
    ((entry & 0x0020_0000_0000_0000) >> 53) as u8
}

fn get_avl(entry: u64) -> u8 {
    ((entry & 0x0010_0000_0000_0000) >> 52) as u8
}

fn get_p(entry: u64) -> u8 {
    ((entry & 0x0000_8000_0000_0000) >> 47) as u8
}

fn get_dpl(entry: u64) -> u8 {
    ((entry & 0x0000_6000_0000_0000) >> 45) as u8
}

fn get_s(entry: u64) -> u8 {
    ((entry & 0x0000_1000_0000_0000) >> 44) as u8
}

fn get_type(entry: u64) -> u8 {
    ((entry & 0x0000_0F00_0000_0000) >> 40) as u8
}

pub fn encode_kvm_segment(segment: &kvm_segment) -> u64 {
    let mut entry: u64 = 0;
    entry |= segment.base as u64;
    entry |= ((segment.limit & 0xFFFF) as u64) << 16;
    entry |= ((segment.selector & 0xFFFF) as u64) << 32;
    entry |= ((segment.type_ & 0xF) as u64) << 40;
    entry |= ((segment.present & 0x1) as u64) << 47;
    entry |= ((segment.dpl & 0x3) as u64) << 45;
    entry |= ((segment.db & 0x1) as u64) << 44;
    entry |= ((segment.s & 0x1) as u64) << 43;
    entry |= ((segment.l & 0x1) as u64) << 42;
    entry |= ((segment.g & 0x1) as u64) << 41;
    entry |= ((segment.avl & 0x1) as u64) << 52;
    entry |= ((segment.unusable & 0xFF) as u64) << 48;
    entry |= ((segment.padding & 0xFF) as u64) << 56;
    entry
}

/// Automatically build the kvm struct for SET_SREGS from the kernel bit fields.
///
/// # Arguments
///
/// * `entry` - The gdt entry.
/// * `table_index` - Index of the entry in the gdt table.
pub fn kvm_segment_from_gdt(entry: u64, table_index: u8) -> kvm_segment {
    kvm_segment {
        base: get_base(entry),
        limit: get_limit(entry),
        selector: u16::from(table_index * 8),
        type_: get_type(entry),
        present: get_p(entry),
        dpl: get_dpl(entry),
        db: get_db(entry),
        s: get_s(entry),
        l: get_l(entry),
        g: get_g(entry),
        avl: get_avl(entry),
        padding: 0,
        unusable: match get_p(entry) {
            0 => 1,
            _ => 0,
        },
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn field_parse() {
        let gdt = gdt_entry(0xA09B, 0x10_0000, 0xfffff);
        let seg = kvm_segment_from_gdt(gdt, 0);
        // 0xA09B
        // 'A'
        assert_eq!(0x1, seg.g);
        assert_eq!(0x0, seg.db);
        assert_eq!(0x1, seg.l);
        assert_eq!(0x0, seg.avl);
        // '9'
        assert_eq!(0x1, seg.present);
        assert_eq!(0x0, seg.dpl);
        assert_eq!(0x1, seg.s);
        // 'B'
        assert_eq!(0xB, seg.type_);
        // base and limit
        assert_eq!(0x10_0000, seg.base);
        assert_eq!(0xfffff, seg.limit);
        assert_eq!(0x0, seg.unusable);
    }
}
