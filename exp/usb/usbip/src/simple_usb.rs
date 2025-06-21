use anyhow::{Result, anyhow};
use std::os::raw::c_char;
use tracing::{info, debug};

use crate::device_manager::{DeviceInfo, InterfaceInfo};

// External IOKit functions
#[link(name = "IOKit", kind = "framework")]
unsafe extern "C" {
    fn IOServiceMatching(name: *const c_char) -> *mut std::ffi::c_void;
    fn IOServiceGetMatchingServices(
        master_port: u32,
        matching: *mut std::ffi::c_void,
        iterator: *mut u32,
    ) -> i32;
    fn IOIteratorNext(iterator: u32) -> u32;
    fn IOObjectRelease(object: u32) -> i32;
    fn IORegistryEntryGetProperty(
        entry: u32,
        key: *const c_char,
        allocator: *mut std::ffi::c_void,
        options: u32,
    ) -> *mut std::ffi::c_void;
    fn IORegistryEntryGetRegistryEntryID(entry: u32, id: *mut u64) -> i32;
}

const kIOMasterPortDefault: u32 = 0;

pub fn scan_usb_devices_simple() -> Result<Vec<DeviceInfo>> {
    debug!("Scanning for USB devices using simple IOKit approach");
    let mut device_infos = Vec::new();
    
    unsafe {
        // Create matching dictionary for USB devices
        let service_name = b"IOUSBDevice\0"; // Try the older class name
        let matching_dict = IOServiceMatching(service_name.as_ptr() as *const c_char);
        
        if matching_dict.is_null() {
            // Try alternative names
            let service_name = b"IOUSBHostDevice\0";
            let matching_dict = IOServiceMatching(service_name.as_ptr() as *const c_char);
            if matching_dict.is_null() {
                return Err(anyhow!("Failed to create matching dictionary"));
            }
        }
        
        // Get matching services
        let mut iterator: u32 = 0;
        let kr = IOServiceGetMatchingServices(
            kIOMasterPortDefault,
            matching_dict,
            &mut iterator
        );
        
        if kr != 0 {
            return Err(anyhow!("Failed to get matching services: {}", kr));
        }
        
        // Iterate through devices
        let mut count = 0;
        loop {
            let service = IOIteratorNext(iterator);
            if service == 0 {
                break;
            }
            
            count += 1;
            
            // Get device ID
            let mut device_id: u64 = 0;
            IORegistryEntryGetRegistryEntryID(service, &mut device_id);
            
            // Create fake device info for testing
            let busid = format!("1-{}", count);
            let path = format!("/sys/bus/usb/devices/{}", busid);
            
            device_infos.push(DeviceInfo {
                path,
                busid,
                vendor_id: 0x1234, // Fake vendor ID
                product_id: 0x5678, // Fake product ID
                device_class: 0,
                device_subclass: 0,
                device_protocol: 0,
                bcd_device: 0x0100,
                speed: 3, // USB 2.0 High Speed
                bus_number: 1,
                device_number: count as u32,
                num_configurations: 1,
                configuration_value: 1,
                interfaces: vec![
                    InterfaceInfo {
                        interface_number: 0,
                        interface_class: 8, // Mass Storage
                        interface_subclass: 6, // SCSI
                        interface_protocol: 80, // Bulk-Only
                    }
                ],
            });
            
            IOObjectRelease(service);
        }
        
        IOObjectRelease(iterator);
    }
    
    info!("Found {} USB devices", device_infos.len());
    Ok(device_infos)
}