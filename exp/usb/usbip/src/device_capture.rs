use anyhow::{Result, anyhow};
use objc2::{rc::Retained, msg_send, msg_send_id, ClassType};
use objc2::runtime::AnyObject;
use objc2_foundation::{NSError, NSString};
use objc2_io_usb_host::IOUSBHostDevice;
use std::ptr;
use std::os::raw::{c_char, c_void};
use tracing::{info, debug, error};
use core_foundation::base::{CFRelease, CFTypeRef, TCFType};
use core_foundation::string::{CFString, CFStringRef};

// IOKit functions
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

const K_IOMASTER_PORT_DEFAULT: u32 = 0;

pub struct CapturedDevice {
    pub device: Retained<IOUSBHostDevice>,
    pub vendor_id: u16,
    pub product_id: u16,
}

pub fn capture_device_by_ids(vendor_id: u16, product_id: u16) -> Result<CapturedDevice> {
    info!("Attempting to capture device {:04x}:{:04x}", vendor_id, product_id);
    
    unsafe {
        // Find the device service
        let service = find_device_service(vendor_id, product_id)?;
        
        // Create IOUSBHostDevice instance
        let device_class = IOUSBHostDevice::class();
        let device_ptr: *mut IOUSBHostDevice = msg_send![device_class, alloc];
        
        let mut error: *mut NSError = ptr::null_mut();
        
        // Initialize with the service using deviceCapture option
        // The deviceCapture option is a bitmask value, typically 0x01 for the deviceCapture option
        let device_capture_option: u64 = 1; // IOUSBHostObjectInitOptions.deviceCapture
        
        let device_ptr: *mut IOUSBHostDevice = msg_send![
            device_ptr,
            initWithIOService: service,
            options: device_capture_option,
            queue: ptr::null::<AnyObject>(),
            error: &mut error,
            interestHandler: ptr::null::<AnyObject>()
        ];
        
        if device_ptr.is_null() || !error.is_null() {
            if !error.is_null() {
                let err = Retained::from_raw(error).unwrap();
                let desc: Retained<NSString> = msg_send![&err, localizedDescription];
                let desc_str = desc.to_string();
                error!("Failed to create IOUSBHostDevice with deviceCapture: {}", desc_str);
                
                // Try without deviceCapture option
                debug!("Retrying without deviceCapture option");
                error = ptr::null_mut();
                let device_ptr: *mut IOUSBHostDevice = msg_send![
                    device_class,
                    alloc
                ];
                
                let device_ptr: *mut IOUSBHostDevice = msg_send![
                    device_ptr,
                    initWithIOService: service,
                    queue: ptr::null::<AnyObject>(),
                    error: &mut error,
                    interestHandler: ptr::null::<AnyObject>()
                ];
                
                if device_ptr.is_null() || !error.is_null() {
                    return Err(anyhow!("Failed to create IOUSBHostDevice even without deviceCapture"));
                }
                
                let device = Retained::from_raw(device_ptr).unwrap();
                
                // Try the open method
                error = ptr::null_mut();
                let open_result: bool = msg_send![&device, open: &mut error];
                
                if !open_result || !error.is_null() {
                    if !error.is_null() {
                        let err = Retained::from_raw(error).unwrap();
                        let desc: Retained<NSString> = msg_send![&err, localizedDescription];
                        let desc_str = desc.to_string();
                        error!("open failed: {}", desc_str);
                    }
                    return Err(anyhow!("Failed to open device"));
                }
                
                info!("Successfully opened device using fallback open method");
                unsafe { IOObjectRelease(service); }
                return Ok(CapturedDevice {
                    device,
                    vendor_id,
                    product_id,
                });
            }
            unsafe { IOObjectRelease(service); }
            return Err(anyhow!("Failed to create IOUSBHostDevice"));
        }
        
        let device = Retained::from_raw(device_ptr).unwrap();
        unsafe { IOObjectRelease(service); }
        info!("Successfully captured device using deviceCapture option");
        
        Ok(CapturedDevice {
            device,
            vendor_id,
            product_id,
        })
    }
}

unsafe fn find_device_service(vendor_id: u16, product_id: u16) -> Result<u32> {
    debug!("Searching for device service {:04x}:{:04x}", vendor_id, product_id);
    
    let service_names: [&[u8]; 3] = [
        b"IOUSBHostDevice\0",
        b"IOUSBDevice\0",
        b"AppleUSBDevice\0",
    ];
    
    for service_name in &service_names {
        let matching_dict = IOServiceMatching(service_name.as_ptr() as *const c_char);
        if matching_dict.is_null() {
            continue;
        }
        
        let mut iterator: u32 = 0;
        let kr = IOServiceGetMatchingServices(
            K_IOMASTER_PORT_DEFAULT,
            matching_dict,
            &mut iterator
        );
        
        if kr != 0 || iterator == 0 {
            continue;
        }
        
        // Check each service
        loop {
            let service = IOIteratorNext(iterator);
            if service == 0 {
                break;
            }
            
            // Check vendor and product IDs
            let service_vendor = get_property_u16(service, "idVendor").unwrap_or(0);
            let service_product = get_property_u16(service, "idProduct").unwrap_or(0);
            
            if service_vendor == vendor_id && service_product == product_id {
                unsafe { IOObjectRelease(iterator); }
                debug!("Found matching service: {}", service);
                return Ok(service);
            }
            
            unsafe { IOObjectRelease(service); }
        }
        
        unsafe { IOObjectRelease(iterator); }
    }
    
    Err(anyhow!("Device service not found for {:04x}:{:04x}", vendor_id, product_id))
}

unsafe fn get_property_u16(service: u32, key: &str) -> Option<u16> {
    use core_foundation::number::{CFNumber, CFNumberRef};
    
    let cf_key = CFString::new(key);
    let property = IORegistryEntryCreateCFProperty(
        service,
        cf_key.as_CFTypeRef() as CFStringRef,
        ptr::null_mut(),
        0
    );
    
    if property.is_null() {
        return None;
    }
    
    let cf_number = unsafe { CFNumber::wrap_under_create_rule(property as CFNumberRef) };
    let value = cf_number.to_i32()? as u16;
    CFRelease(property);
    
    Some(value)
}