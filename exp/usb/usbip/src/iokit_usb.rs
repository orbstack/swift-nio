use anyhow::{Result, anyhow};
use objc2::{rc::Retained, msg_send, ClassType};
use objc2::runtime::AnyObject;
use objc2_foundation::NSError;
use objc2_io_usb_host::IOUSBHostDevice;
use objc2_io_kit::{IOUSBDeviceDescriptor, IOUSBConfigurationDescriptor, IOUSBInterfaceDescriptor};
use std::ptr;
use std::os::raw::c_char;
use tracing::{info, debug};

use crate::device_manager::{DeviceInfo, InterfaceInfo};

// External IOKit functions
unsafe extern "C" {
    fn IOServiceMatching(name: *const c_char) -> *mut AnyObject;
    fn IOServiceGetMatchingServices(
        master_port: u32,
        matching: *mut AnyObject,
        iterator: *mut u32,
    ) -> i32;
    fn IOIteratorNext(iterator: u32) -> u32;
    fn IOObjectRelease(object: u32) -> i32;
}

const kIOMasterPortDefault: u32 = 0;

pub fn scan_usb_devices() -> Result<Vec<DeviceInfo>> {
    debug!("Scanning for USB devices using IOKit");
    let mut device_infos = Vec::new();
    
    unsafe {
        // Create matching dictionary for IOUSBHostDevice
        let service_name = b"IOUSBHostDevice\0";
        let matching_dict = IOServiceMatching(service_name.as_ptr() as *const c_char);
        
        if matching_dict.is_null() {
            return Err(anyhow!("Failed to create matching dictionary"));
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
        let device_class = IOUSBHostDevice::class();
        
        loop {
            let service = IOIteratorNext(iterator);
            if service == 0 {
                break;
            }
            
            // Try to create IOUSBHostDevice from service
            let device_ptr: *mut IOUSBHostDevice = msg_send![
                device_class,
                alloc
            ];
            
            if !device_ptr.is_null() {
                let mut error: *mut NSError = ptr::null_mut();
                // Try the simpler init method
                let device_ptr: *mut IOUSBHostDevice = msg_send![
                    device_ptr,
                    initWithIOService: service,
                    error: &mut error
                ];
                
                if !device_ptr.is_null() && error.is_null() {
                    let device = Retained::from_raw(device_ptr).unwrap();
                    if let Ok(info) = get_device_info(&device) {
                        device_infos.push(info);
                    }
                }
            }
            
            IOObjectRelease(service);
        }
        
        IOObjectRelease(iterator);
    }
    
    info!("Found {} USB devices", device_infos.len());
    Ok(device_infos)
}

fn get_device_info(device: &IOUSBHostDevice) -> Result<DeviceInfo> {
    unsafe {
        // Get device descriptor
        let descriptor_ptr: *const IOUSBDeviceDescriptor = msg_send![device, deviceDescriptor];
        if descriptor_ptr.is_null() {
            return Err(anyhow!("No device descriptor"));
        }
        let descriptor = &*descriptor_ptr;
        
        // Get vendor and product IDs
        let vendor_id = descriptor.idVendor;
        let product_id = descriptor.idProduct;
        
        // Get device class info
        let device_class = descriptor.bDeviceClass;
        let device_subclass = descriptor.bDeviceSubClass;
        let device_protocol = descriptor.bDeviceProtocol;
        let bcd_device = descriptor.bcdDevice;
        
        // Get speed
        let speed: u32 = msg_send![device, speed];
        
        // Get bus and device numbers
        let location_id: u32 = msg_send![device, locationID];
        let bus_number = (location_id >> 24) & 0xFF;
        let device_number = location_id & 0xFF;
        
        // Get configuration info
        let num_configurations = descriptor.bNumConfigurations;
        let config_desc_ptr: *const IOUSBConfigurationDescriptor = msg_send![device, configurationDescriptor];
        let configuration_value = if config_desc_ptr.is_null() { 0 } else { (*config_desc_ptr).bConfigurationValue };
        
        // Create busid (simplified - should match Linux format)
        let busid = format!("{}-{}", bus_number, device_number);
        let path = format!("/sys/bus/usb/devices/{}", busid);
        
        // Get interfaces
        let mut interfaces = Vec::new();
        if !config_desc_ptr.is_null() {
            let num_interfaces = (*config_desc_ptr).bNumInterfaces;
            for i in 0..num_interfaces {
                // Try to get interface descriptor - but don't use objc2 methods
                let interface_ptr: *mut AnyObject = msg_send![
                    device,
                    getInterfaceWithInterfaceNumber: i
                ];
                
                if !interface_ptr.is_null() {
                    let iface_desc_ptr: *const IOUSBInterfaceDescriptor = msg_send![interface_ptr, interfaceDescriptor];
                    if !iface_desc_ptr.is_null() {
                        interfaces.push(InterfaceInfo {
                            interface_number: i,
                            interface_class: (*iface_desc_ptr).bInterfaceClass,
                            interface_subclass: (*iface_desc_ptr).bInterfaceSubClass,
                            interface_protocol: (*iface_desc_ptr).bInterfaceProtocol,
                        });
                    }
                    
                    // Release the interface reference
                    let _: () = msg_send![interface_ptr, release];
                }
            }
        }
        
        Ok(DeviceInfo {
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
        })
    }
}