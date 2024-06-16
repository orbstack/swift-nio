// Copyright 2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Portions Copyright 2017 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the THIRD-PARTY file.

//! Handles routing to devices in an address space.

use smallbox::SmallBox;
use std::any::type_name;
use std::cmp::{Ord, Ordering, PartialEq, PartialOrd};
use std::collections::btree_map::BTreeMap;
use std::fmt;
use std::io;
use std::result;
use std::sync::Arc;
use utils::Mutex;

use rustc_hash::FxHashMap;
use vm_memory::GuestAddress;

use crate::virtio::HvcDevice;

// === LocklessBusDevice === //

/// A type-erased [`LocklessBusDevice`]
pub struct ErasedBusDevice(SmallBox<dyn LocklessBusDevice, *const dyn LocklessBusDevice>);

impl fmt::Debug for ErasedBusDevice {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_tuple("BusDeviceHandle")
            .field(&self.0.debug_name())
            .finish()
    }
}

impl Clone for ErasedBusDevice {
    fn clone(&self) -> Self {
        self.0.clone_erased()
    }
}

impl ErasedBusDevice {
    pub fn new(handle: impl LocklessBusDevice) -> Self {
        Self(smallbox::smallbox!(handle))
    }
}

impl LocklessBusDevice for ErasedBusDevice {
    fn read(&self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.0.read(vcpuid, offset, data)
    }

    fn write(&self, vcpuid: u64, offset: u64, data: &[u8]) {
        self.0.write(vcpuid, offset, data)
    }

    fn interrupt(&self, irq_mask: u32) -> io::Result<()> {
        self.0.interrupt(irq_mask)
    }

    fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        self.0.read_sysreg(vcpuid, reg)
    }

    fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        self.0.write_sysreg(vcpuid, reg, value)
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        self.0.iter_sysregs()
    }

    fn clone_erased(&self) -> ErasedBusDevice {
        self.0.clone_erased()
    }

    fn debug_name(&self) -> &'static str {
        self.0.debug_name()
    }

    fn erase(self) -> ErasedBusDevice
    where
        Self: Sized,
    {
        self
    }
}

/// A variant of [`BusDevice`] which assumes that operations can be performed without external
/// synchronization.
#[allow(unused_variables)]
pub trait LocklessBusDevice: 'static + Send + Sync {
    fn read(&self, vcpuid: u64, offset: u64, data: &mut [u8]) {}

    fn write(&self, vcpuid: u64, offset: u64, data: &[u8]) {}

    fn interrupt(&self, irq_mask: u32) -> io::Result<()> {
        Ok(())
    }

    fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        unimplemented!()
    }

    fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        unimplemented!()
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        Vec::new()
    }

    fn clone_erased(&self) -> ErasedBusDevice;

    fn debug_name(&self) -> &'static str {
        type_name::<Self>()
    }

    fn erase(self) -> ErasedBusDevice
    where
        Self: Sized,
    {
        ErasedBusDevice::new(self)
    }
}

// === Locked Bus Device === //

/// Trait for devices that respond to reads or writes in an arbitrary address space.
///
/// The device does not care where it exists in address space as each method is only given an offset
/// into its allocated portion of address space.
#[allow(unused_variables)]
pub trait BusDevice: Send {
    /// Reads at `offset` from this device
    fn read(&mut self, vcpuid: u64, offset: u64, data: &mut [u8]) {}

    /// Writes at `offset` into this device
    fn write(&mut self, vcpuid: u64, offset: u64, data: &[u8]) {}

    /// Triggers the `irq_mask` interrupt on this device
    fn interrupt(&self, irq_mask: u32) -> io::Result<()> {
        Ok(())
    }

    fn read_sysreg(&mut self, vcpuid: u64, reg: u64) -> u64 {
        unimplemented!();
    }

    fn write_sysreg(&mut self, vcpuid: u64, reg: u64, value: u64) {
        unimplemented!();
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        Vec::new()
    }
}

impl<T: 'static + BusDevice> LocklessBusDevice for Arc<Mutex<T>> {
    fn read(&self, vcpuid: u64, offset: u64, data: &mut [u8]) {
        self.lock().unwrap().read(vcpuid, offset, data)
    }

    fn write(&self, vcpuid: u64, offset: u64, data: &[u8]) {
        self.lock().unwrap().write(vcpuid, offset, data)
    }

    fn interrupt(&self, irq_mask: u32) -> io::Result<()> {
        self.lock().unwrap().interrupt(irq_mask)
    }

    fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        self.lock().unwrap().read_sysreg(vcpuid, reg)
    }

    fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        self.lock().unwrap().write_sysreg(vcpuid, reg, value)
    }

    fn iter_sysregs(&self) -> Vec<u64> {
        self.lock().unwrap().iter_sysregs()
    }

    fn clone_erased(&self) -> ErasedBusDevice {
        ErasedBusDevice::new(self.clone())
    }

    fn debug_name(&self) -> &'static str {
        type_name::<T>()
    }
}

// === Bus === //

#[derive(Debug)]
pub enum Error {
    /// The insertion failed because the new device overlapped with an old device.
    Overlap,
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter) -> fmt::Result {
        use self::Error::*;

        match *self {
            Overlap => write!(f, "New device overlaps with an old device."),
        }
    }
}

pub type Result<T> = result::Result<T, Error>;

#[derive(Debug, Copy, Clone)]
struct BusRange(u64, u64);

impl Eq for BusRange {}

impl PartialEq for BusRange {
    fn eq(&self, other: &BusRange) -> bool {
        self.0 == other.0
    }
}

impl Ord for BusRange {
    fn cmp(&self, other: &BusRange) -> Ordering {
        self.0.cmp(&other.0)
    }
}

impl PartialOrd for BusRange {
    fn partial_cmp(&self, other: &BusRange) -> Option<Ordering> {
        Some(self.cmp(other))
    }
}

/// A device container for routing reads and writes over some address space.
///
/// This doesn't have any restrictions on what kind of device or address space this applies to. The
/// only restriction is that no two devices can overlap in this address space.
#[derive(Clone, Default)]
pub struct Bus {
    devices: BTreeMap<BusRange, ErasedBusDevice>,
    sysreg_handlers: FxHashMap<u64, ErasedBusDevice>,
    hvc_handlers: BTreeMap<usize, Arc<dyn HvcDevice>>,
}

impl Bus {
    /// Constructs an a bus with an empty address space.
    pub fn new() -> Bus {
        Bus {
            devices: BTreeMap::new(),
            sysreg_handlers: FxHashMap::default(),
            hvc_handlers: BTreeMap::new(),
        }
    }

    fn first_before(&self, addr: u64) -> Option<(BusRange, &ErasedBusDevice)> {
        // for when we switch to rustc 1.17: self.devices.range(..addr).iter().rev().next()
        for (range, dev) in self.devices.iter().rev() {
            if range.0 <= addr {
                return Some((*range, dev));
            }
        }
        None
    }

    pub fn get_device(&self, addr: u64) -> Option<(u64, &ErasedBusDevice)> {
        if let Some((BusRange(start, len), dev)) = self.first_before(addr) {
            let offset = addr - start;
            if offset < len {
                return Some((offset, dev));
            }
        }
        None
    }

    /// Puts the given device at the given address space.
    pub fn insert(&mut self, device: impl LocklessBusDevice, base: u64, len: u64) -> Result<()> {
        let device = device.erase();

        if len == 0 {
            return Err(Error::Overlap);
        }

        // Reject all system register overlaps
        let sys_regs = device.iter_sysregs();
        if sys_regs
            .iter()
            .any(|sys_reg| self.sysreg_handlers.contains_key(sys_reg))
        {
            return Err(Error::Overlap);
        }

        // Reject all cases where the new device's base is within an old device's range.
        if self.get_device(base).is_some() {
            return Err(Error::Overlap);
        }

        // The above check will miss an overlap in which the new device's base address is before the
        // range of another device. To catch that case, we search for a device with a range before
        // the new device's range's end. If there is no existing device in that range that starts
        // after the new device, then there will be no overlap.
        if let Some((BusRange(start, _), _)) = self.first_before(base + len - 1) {
            // Such a device only conflicts with the new device if it also starts after the new
            // device because of our initial `get_device` check above.
            if start >= base {
                return Err(Error::Overlap);
            }
        }

        for sys_reg in sys_regs {
            self.sysreg_handlers.insert(sys_reg, device.clone());
        }

        if self.devices.insert(BusRange(base, len), device).is_some() {
            return Err(Error::Overlap);
        }

        Ok(())
    }

    pub fn insert_hvc(&mut self, device: Arc<dyn HvcDevice>) -> Result<()> {
        if let Some(hvc_id) = device.hvc_id() {
            if self.hvc_handlers.contains_key(&hvc_id) {
                return Err(Error::Overlap);
            }

            self.hvc_handlers.insert(hvc_id, device);
        }

        Ok(())
    }

    /// Reads data from the device that owns the range containing `addr` and puts it into `data`.
    ///
    /// Returns true on success, otherwise `data` is untouched.
    pub fn read(&self, vcpuid: u64, addr: u64, data: &mut [u8]) -> bool {
        if let Some((offset, dev)) = self.get_device(addr) {
            dev.read(vcpuid, offset, data);
            true
        } else {
            false
        }
    }

    /// Writes `data` to the device that owns the range containing `addr`.
    ///
    /// Returns true on success, otherwise `data` is untouched.
    pub fn write(&self, vcpuid: u64, addr: u64, data: &[u8]) -> bool {
        if let Some((offset, dev)) = self.get_device(addr) {
            dev.write(vcpuid, offset, data);
            true
        } else {
            false
        }
    }

    pub fn read_sysreg(&self, vcpuid: u64, reg: u64) -> u64 {
        if let Some(handler) = self.sysreg_handlers.get(&reg) {
            handler.read_sysreg(vcpuid, reg)
        } else {
            // tracing::warn!("Unhandled read to from register for PE {vcpuid}: READ from {reg}");
            0
        }
    }

    pub fn write_sysreg(&self, vcpuid: u64, reg: u64, value: u64) {
        if let Some(handler) = self.sysreg_handlers.get(&reg) {
            handler.write_sysreg(vcpuid, reg, value);
        } else {
            // tracing::warn!(
            //     "Unhandled write to system register for PE {vcpuid}: WRITE {value} to {reg}"
            // );
        }
    }

    pub fn call_hvc(&self, dev_id: usize, args_addr: GuestAddress) -> i64 {
        if let Some(handler) = self.hvc_handlers.get(&dev_id) {
            handler.call_hvc(args_addr)
        } else {
            error!("unhandled io HVC call");
            -1
        }
    }
}

/*
#[cfg(test)]
mod tests {
    use super::*;

    struct DummyDevice;
    impl BusDevice for DummyDevice {}

    struct ConstantDevice;
    impl BusDevice for ConstantDevice {
        fn read(&mut self, _vcpuid: u64, offset: u64, data: &mut [u8]) {
            for (i, v) in data.iter_mut().enumerate() {
                *v = (offset as u8) + (i as u8);
            }
        }

        fn write(&mut self, _vcpuid: u64, offset: u64, data: &[u8]) {
            for (i, v) in data.iter().enumerate() {
                assert_eq!(*v, (offset as u8) + (i as u8))
            }
        }
    }

    #[test]
    fn bus_insert() {
        let mut bus = Bus::new();
        let dummy = Arc::new(Mutex::new(DummyDevice));
        // Insert len should not be 0.
        assert!(bus.insert(dummy.clone(), 0x10, 0).is_err());
        assert!(bus.insert(dummy.clone(), 0x10, 0x10).is_ok());

        let result = bus.insert(dummy.clone(), 0x0f, 0x10);
        // This overlaps the address space of the existing bus device at 0x10.
        assert!(result.is_err());
        assert_eq!(format!("{:?}", result), "Err(Overlap)");

        // This overlaps the address space of the existing bus device at 0x10.
        assert!(bus.insert(dummy.clone(), 0x10, 0x10).is_err());
        // This overlaps the address space of the existing bus device at 0x10.
        assert!(bus.insert(dummy.clone(), 0x10, 0x15).is_err());
        // This overlaps the address space of the existing bus device at 0x10.
        assert!(bus.insert(dummy.clone(), 0x12, 0x15).is_err());
        // This overlaps the address space of the existing bus device at 0x10.
        assert!(bus.insert(dummy.clone(), 0x12, 0x01).is_err());
        // This overlaps the address space of the existing bus device at 0x10.
        assert!(bus.insert(dummy.clone(), 0x0, 0x20).is_err());
        assert!(bus.insert(dummy.clone(), 0x20, 0x05).is_ok());
        assert!(bus.insert(dummy.clone(), 0x25, 0x05).is_ok());
        assert!(bus.insert(dummy, 0x0, 0x10).is_ok());
    }

    #[test]
    fn bus_read_write() {
        let mut bus = Bus::new();
        let dummy = Arc::new(Mutex::new(DummyDevice));
        assert!(bus.insert(dummy, 0x10, 0x10).is_ok());
        assert!(bus.read(0, 0x10, &mut [0, 0, 0, 0]));
        assert!(bus.write(0, 0x10, &[0, 0, 0, 0]));
        assert!(bus.read(0, 0x11, &mut [0, 0, 0, 0]));
        assert!(bus.write(0, 0x11, &[0, 0, 0, 0]));
        assert!(bus.read(0, 0x16, &mut [0, 0, 0, 0]));
        assert!(bus.write(0, 0x16, &[0, 0, 0, 0]));
        assert!(!bus.read(0, 0x20, &mut [0, 0, 0, 0]));
        assert!(!bus.write(0, 0x20, &[0, 0, 0, 0]));
        assert!(!bus.read(0, 0x06, &mut [0, 0, 0, 0]));
        assert!(!bus.write(0, 0x06, &[0, 0, 0, 0]));
    }

    #[test]
    fn bus_read_write_values() {
        let mut bus = Bus::new();
        let dummy = Arc::new(Mutex::new(ConstantDevice));
        assert!(bus.insert(dummy, 0x10, 0x10).is_ok());

        let mut values = [0, 1, 2, 3];
        assert!(bus.read(0, 0x10, &mut values));
        assert_eq!(values, [0, 1, 2, 3]);
        assert!(bus.write(0, 0x10, &values));
        assert!(bus.read(0, 0x15, &mut values));
        assert_eq!(values, [5, 6, 7, 8]);
        assert!(bus.write(0, 0x15, &values));
    }

    #[test]
    fn busrange_cmp_and_clone() {
        assert_eq!(BusRange(0x10, 2), BusRange(0x10, 3));
        assert_eq!(BusRange(0x10, 2), BusRange(0x10, 2));

        assert!(BusRange(0x10, 2) < BusRange(0x12, 1));
        assert!(BusRange(0x10, 2) < BusRange(0x12, 3));

        let bus_range = BusRange(0x10, 2);
        assert_eq!(bus_range, BusRange(0x10, 2));

        let mut bus = Bus::new();
        let mut data = [1, 2, 3, 4];
        assert!(bus
            .insert(Arc::new(Mutex::new(DummyDevice)), 0x10, 0x10)
            .is_ok());
        assert!(bus.write(0, 0x10, &data));
        let bus_clone = bus.clone();
        assert!(bus.read(0, 0x10, &mut data));
        assert_eq!(data, [1, 2, 3, 4]);
        assert!(bus_clone.read(0, 0x10, &mut data));
        assert_eq!(data, [1, 2, 3, 4]);
    }

    #[test]
    fn test_display_error() {
        assert_eq!(
            format!("{}", Error::Overlap),
            "New device overlaps with an old device."
        );
    }
}
*/
