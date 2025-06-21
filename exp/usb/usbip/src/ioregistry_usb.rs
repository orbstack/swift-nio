use anyhow::{Result, anyhow};
use std::os::raw::{c_char, c_void};
use tracing::{info, debug};
use core_foundation::base::{CFRelease, CFTypeRef, TCFType};
use core_foundation::number::{CFNumber, CFNumberRef};
use core_foundation::string::{CFString, CFStringRef};

use crate::device_manager::{DeviceInfo, InterfaceInfo};

// External IOKit functions
#[link(name = "IOKit", kind = "framework")]
unsafe extern "C" {
    fn IOServiceMatching(name: *const c_char) -> *mut c_void;
    fn IOServiceGetMatchingServices(
        master_port: u32,
        matching: *mut c_void,
        iterator: *mut u32,
    ) -> i32;
    fn IOIteratorNext(iterator: u32) -> u32;
    fn IOObjectRelease(object: u32) -> i32;
    fn IORegistryEntryCreateCFProperty(
        entry: u32,
        key: CFStringRef,
        allocator: *mut c_void,
        options: u32,
    ) -> CFTypeRef;
}

const kIOMasterPortDefault: u32 = 0;

pub fn scan_usb_devices_registry() -> Result<Vec<DeviceInfo>> {
    debug!("Scanning for USB devices using IORegistry");
    let mut device_infos = Vec::new();
    
    unsafe {
        // Try multiple service names
        let service_names: [&[u8]; 3] = [
            b"IOUSBHostDevice\0",
            b"IOUSBDevice\0",
            b"AppleUSBDevice\0",
        ];
        
        let mut iterator: u32 = 0;
        let mut found_services = false;
        
        for service_name in &service_names {
            let matching_dict = IOServiceMatching(service_name.as_ptr() as *const c_char);
            
            if matching_dict.is_null() {
                continue;
            }
            
            let kr = IOServiceGetMatchingServices(
                kIOMasterPortDefault,
                matching_dict,
                &mut iterator
            );
            
            if kr == 0 && iterator != 0 {
                found_services = true;
                debug!("Found USB services with class: {:?}", std::str::from_utf8(service_name));
                break;
            }
        }
        
        if !found_services {
            return Err(anyhow!("No USB services found"));
        }
        
        // Iterate through devices
        let mut device_count = 0;
        loop {
            let service = IOIteratorNext(iterator);
            if service == 0 {
                break;
            }
            
            device_count += 1;
            
            // Get device properties from IORegistry
            let vendor_id = get_property_u16(service, "idVendor").unwrap_or(0);
            let product_id = get_property_u16(service, "idProduct").unwrap_or(0);
            let device_class = get_property_u8(service, "bDeviceClass").unwrap_or(0);
            let device_subclass = get_property_u8(service, "bDeviceSubClass").unwrap_or(0);
            let device_protocol = get_property_u8(service, "bDeviceProtocol").unwrap_or(0);
            let bcd_device = get_property_u16(service, "bcdDevice").unwrap_or(0);
            let location_id = get_property_u32(service, "locationID").unwrap_or(device_count);
            
            // Extract bus and device numbers from location ID
            let bus_number = ((location_id >> 24) & 0xFF).max(1);
            let device_number = (location_id & 0xFF).max(device_count);
            
            // Create busid
            let busid = format!("{}-{}", bus_number, device_number);
            let path = format!("/sys/bus/usb/devices/{}", busid);
            
            // Get configuration info
            let num_configurations = get_property_u8(service, "bNumConfigurations").unwrap_or(1);
            let configuration_value = get_property_u8(service, "bConfigurationValue").unwrap_or(1);
            
            // Get speed (USB_SPEED_* constants)
            let speed = get_property_u32(service, "Device Speed").unwrap_or(3); // Default to high speed
            
            // For now, create a default interface
            // In a real implementation, we'd enumerate interfaces from IORegistry
            let interfaces = vec![
                InterfaceInfo {
                    interface_number: 0,
                    interface_class: 8, // Mass Storage
                    interface_subclass: 6, // SCSI
                    interface_protocol: 80, // Bulk-Only
                }
            ];
            
            info!("Found USB device: {:04x}:{:04x} at {}", vendor_id, product_id, busid);
            info!("  Class: {:02x}, SubClass: {:02x}, Protocol: {:02x}", device_class, device_subclass, device_protocol);
            info!("  bcdDevice: {:04x}, configs: {}", bcd_device, num_configurations);
            
            device_infos.push(DeviceInfo {
                path,
                busid,
                vendor_id,
                product_id,
                device_class,
                device_subclass,
                device_protocol,
                bcd_device,
                speed,
                bus_number,
                device_number,
                num_configurations,
                configuration_value,
                interfaces,
            });
            
            IOObjectRelease(service);
        }
        
        IOObjectRelease(iterator);
    }
    
    info!("Found {} USB devices", device_infos.len());
    Ok(device_infos)
}

unsafe fn get_property_u32(service: u32, key: &str) -> Option<u32> {
    unsafe {
        let cf_key = CFString::new(key);
        let property = IORegistryEntryCreateCFProperty(
            service,
            cf_key.as_concrete_TypeRef(),
            std::ptr::null_mut(),
            0
        );
        
        if property.is_null() {
            return None;
        }
        
        let cf_number = CFNumber::wrap_under_create_rule(property as CFNumberRef);
        let value = cf_number.to_i32()? as u32;
        CFRelease(property);
        
        Some(value)
    }
}

unsafe fn get_property_u16(service: u32, key: &str) -> Option<u16> {
    unsafe { get_property_u32(service, key).map(|v| v as u16) }
}

unsafe fn get_property_u8(service: u32, key: &str) -> Option<u8> {
    unsafe { get_property_u32(service, key).map(|v| v as u8) }
}