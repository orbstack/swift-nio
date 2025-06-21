use anyhow::{Result, anyhow};
use objc2::{rc::Retained, msg_send};
use objc2_foundation::{NSError, NSRunLoop, NSDate};
use objc2_io_usb_host::{IOUSBHostDevice, IOUSBHostInterface, IOUSBHostPipe};
use objc2_io_kit::{IOUSBDeviceDescriptor, IOUSBConfigurationDescriptor, IOUSBInterfaceDescriptor, IOUSBEndpointDescriptor};
use std::collections::HashMap;
use tokio::sync::{mpsc, oneshot};
use tracing::{info, debug, error, warn};
use std::ptr;

use crate::proto::*;

// Messages to send to the device manager thread
#[derive(Debug)]
pub enum DeviceCommand {
    ScanDevices {
        reply: oneshot::Sender<Result<Vec<DeviceInfo>>>,
    },
    ImportDevice {
        busid: String,
        reply: oneshot::Sender<Result<ImportedDevice>>,
    },
    SubmitTransfer {
        device_id: u32,
        transfer: TransferRequest,
        reply: oneshot::Sender<Result<TransferResponse>>,
    },
    UnlinkTransfer {
        device_id: u32,
        seqnum: u32,
        reply: oneshot::Sender<Result<()>>,
    },
    ReleaseDevice {
        device_id: u32,
        reply: oneshot::Sender<Result<()>>,
    },
}

#[derive(Debug, Clone)]
pub struct DeviceInfo {
    pub path: String,
    pub busid: String,
    pub vendor_id: u16,
    pub product_id: u16,
    pub device_class: u8,
    pub device_subclass: u8,
    pub device_protocol: u8,
    pub bcd_device: u16,
    pub speed: u32,
    pub bus_number: u32,
    pub device_number: u32,
    pub num_configurations: u8,
    pub configuration_value: u8,
    pub interfaces: Vec<InterfaceInfo>,
}

#[derive(Debug, Clone)]
pub struct InterfaceInfo {
    pub interface_number: u8,
    pub interface_class: u8,
    pub interface_subclass: u8,
    pub interface_protocol: u8,
}

#[derive(Debug)]
pub struct ImportedDevice {
    pub device_id: u32,
    pub info: DeviceInfo,
}

#[derive(Debug)]
pub struct TransferRequest {
    pub seqnum: u32,
    pub direction: u32,
    pub endpoint: u32,
    pub transfer_flags: u32,
    pub transfer_length: u32,
    pub setup: Option<[u8; 8]>,
    pub data: Option<Vec<u8>>,
    pub start_frame: u32,
    pub number_of_packets: u32,
    pub interval: u32,
}

#[derive(Debug)]
pub struct TransferResponse {
    pub seqnum: u32,
    pub status: i32,
    pub actual_length: u32,
    pub data: Option<Vec<u8>>,
    pub start_frame: u32,
    pub number_of_packets: u32,
    pub error_count: u32,
}

struct ManagedDevice {
    device: Retained<IOUSBHostDevice>,
    interfaces: HashMap<u8, Retained<IOUSBHostInterface>>,
    pipes: HashMap<u8, Retained<IOUSBHostPipe>>, // endpoint -> pipe
    info: DeviceInfo,
}

pub struct DeviceManager {
    sender: mpsc::Sender<DeviceCommand>,
}

impl DeviceManager {
    pub fn new() -> Self {
        let (sender, receiver) = mpsc::channel(100);
        
        // Spawn the device manager thread
        std::thread::spawn(move || {
            let mut manager = DeviceManagerThread::new();
            manager.run(receiver);
        });
        
        Self { sender }
    }
    
    pub async fn scan_devices(&self) -> Result<Vec<DeviceInfo>> {
        let (reply, rx) = oneshot::channel();
        self.sender.send(DeviceCommand::ScanDevices { reply }).await
            .map_err(|_| anyhow!("Device manager thread died"))?;
        rx.await.map_err(|_| anyhow!("Device manager thread died"))?
    }
    
    pub async fn import_device(&self, busid: &str) -> Result<ImportedDevice> {
        let (reply, rx) = oneshot::channel();
        self.sender.send(DeviceCommand::ImportDevice {
            busid: busid.to_string(),
            reply,
        }).await.map_err(|_| anyhow!("Device manager thread died"))?;
        rx.await.map_err(|_| anyhow!("Device manager thread died"))?
    }
    
    pub async fn submit_transfer(&self, device_id: u32, transfer: TransferRequest) -> Result<TransferResponse> {
        let (reply, rx) = oneshot::channel();
        self.sender.send(DeviceCommand::SubmitTransfer {
            device_id,
            transfer,
            reply,
        }).await.map_err(|_| anyhow!("Device manager thread died"))?;
        rx.await.map_err(|_| anyhow!("Device manager thread died"))?
    }
    
    pub async fn unlink_transfer(&self, device_id: u32, seqnum: u32) -> Result<()> {
        let (reply, rx) = oneshot::channel();
        self.sender.send(DeviceCommand::UnlinkTransfer {
            device_id,
            seqnum,
            reply,
        }).await.map_err(|_| anyhow!("Device manager thread died"))?;
        rx.await.map_err(|_| anyhow!("Device manager thread died"))?
    }
    
    pub async fn release_device(&self, device_id: u32) -> Result<()> {
        let (reply, rx) = oneshot::channel();
        self.sender.send(DeviceCommand::ReleaseDevice {
            device_id,
            reply,
        }).await.map_err(|_| anyhow!("Device manager thread died"))?;
        rx.await.map_err(|_| anyhow!("Device manager thread died"))?
    }
}

struct DeviceManagerThread {
    devices: HashMap<u32, ManagedDevice>,
    next_device_id: u32,
    busid_to_device_id: HashMap<String, u32>,
}

impl DeviceManagerThread {
    fn new() -> Self {
        Self {
            devices: HashMap::new(),
            next_device_id: 1,
            busid_to_device_id: HashMap::new(),
        }
    }
    
    fn run(&mut self, mut receiver: mpsc::Receiver<DeviceCommand>) {
        info!("Device manager thread started");
        
        // Create run loop for macOS
        unsafe {
            let run_loop = NSRunLoop::currentRunLoop();
            
            // Process commands
            loop {
                // Check for commands with a timeout
                let _deadline = std::time::Instant::now() + std::time::Duration::from_millis(100);
                
                match receiver.try_recv() {
                    Ok(cmd) => self.handle_command(cmd),
                    Err(mpsc::error::TryRecvError::Empty) => {
                        // Run the run loop briefly
                        let date = NSDate::dateWithTimeIntervalSinceNow(0.1);
                        run_loop.runUntilDate(&date);
                    }
                    Err(mpsc::error::TryRecvError::Disconnected) => {
                        info!("Device manager shutting down");
                        break;
                    }
                }
            }
        }
    }
    
    fn handle_command(&mut self, cmd: DeviceCommand) {
        match cmd {
            DeviceCommand::ScanDevices { reply } => {
                let _ = reply.send(self.scan_devices());
            }
            DeviceCommand::ImportDevice { busid, reply } => {
                let _ = reply.send(self.import_device(&busid));
            }
            DeviceCommand::SubmitTransfer { device_id, transfer, reply } => {
                let _ = reply.send(self.submit_transfer(device_id, transfer));
            }
            DeviceCommand::UnlinkTransfer { device_id, seqnum, reply } => {
                let _ = reply.send(self.unlink_transfer(device_id, seqnum));
            }
            DeviceCommand::ReleaseDevice { device_id, reply } => {
                let _ = reply.send(self.release_device(device_id));
            }
        }
    }
    
    fn scan_devices(&mut self) -> Result<Vec<DeviceInfo>> {
        // Use the ioregistry_usb module which doesn't try to claim devices
        crate::ioregistry_usb::scan_usb_devices_registry()
    }
    
    fn get_device_info(&self, device: &IOUSBHostDevice) -> Result<DeviceInfo> {
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
                    // Try to get interface descriptor
                    let interface_ptr: *mut IOUSBHostInterface = msg_send![
                        device,
                        getInterfaceWithInterfaceNumber: i
                    ];
                    
                    if interface_ptr.is_null() {
                        continue;
                    }
                    
                    let interface = Retained::from_raw(interface_ptr).unwrap();
                    
                    let iface_desc_ptr: *const IOUSBInterfaceDescriptor = msg_send![&interface, interfaceDescriptor];
                    if !iface_desc_ptr.is_null() {
                        interfaces.push(InterfaceInfo {
                            interface_number: i,
                            interface_class: (*iface_desc_ptr).bInterfaceClass,
                            interface_subclass: (*iface_desc_ptr).bInterfaceSubClass,
                            interface_protocol: (*iface_desc_ptr).bInterfaceProtocol,
                        });
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
    
    fn import_device(&mut self, busid: &str) -> Result<ImportedDevice> {
        info!("Importing device: {}", busid);
        
        // Check if already imported
        if let Some(&device_id) = self.busid_to_device_id.get(busid) {
            if let Some(device) = self.devices.get(&device_id) {
                return Ok(ImportedDevice {
                    device_id,
                    info: device.info.clone(),
                });
            }
        }
        
        // Find the device
        let devices = self.scan_devices()?;
        let device_info = devices.into_iter()
            .find(|d| d.busid == busid)
            .ok_or_else(|| anyhow!("Device not found: {}", busid))?;
        
        // Use the device_capture module to capture the device
        let captured = crate::device_capture::capture_device_by_ids(
            device_info.vendor_id,
            device_info.product_id
        )?;
        
        let device = captured.device;
        
        unsafe {
            
            // For now, skip interface enumeration as it requires more complex matching
            // TODO: Implement proper interface matching and enumeration
            let interfaces = HashMap::new();
            let pipes = HashMap::new();
            
            info!("Device captured successfully, interface enumeration not yet implemented");
            
            // Store the device
            let device_id = self.next_device_id;
            self.next_device_id += 1;
            
            self.devices.insert(device_id, ManagedDevice {
                device,
                interfaces,
                pipes,
                info: device_info.clone(),
            });
            
            self.busid_to_device_id.insert(busid.to_string(), device_id);
            
            info!("Successfully imported device {} with ID {}", busid, device_id);
            
            Ok(ImportedDevice {
                device_id,
                info: device_info,
            })
        }
    }
    
    fn submit_transfer(&mut self, device_id: u32, transfer: TransferRequest) -> Result<TransferResponse> {
        let device = self.devices.get_mut(&device_id)
            .ok_or_else(|| anyhow!("Device not found: {}", device_id))?;
        
        debug!("Submitting transfer: seq={}, ep={}, len={}", 
               transfer.seqnum, transfer.endpoint, transfer.transfer_length);
        
        // TODO: Properly implement USB transfers once we have interface/pipe enumeration
        debug!("Transfer request - endpoint: {}, direction: {}, length: {}", 
               transfer.endpoint, transfer.direction, transfer.transfer_length);
        
        // For now, return a stub response for control transfers
        if transfer.endpoint == 0 {
            if let Some(setup) = transfer.setup {
                // Parse setup packet
                let request_type = setup[0];
                let request = setup[1];
                let value = u16::from_le_bytes([setup[2], setup[3]]);
                let index = u16::from_le_bytes([setup[4], setup[5]]);
                let length = u16::from_le_bytes([setup[6], setup[7]]);
                
                info!("Control transfer - type: 0x{:02x}, req: 0x{:02x}, val: 0x{:04x}, idx: 0x{:04x}, len: {}", 
                       request_type, request, value, index, length);
                info!("Device info - VID: 0x{:04x}, PID: 0x{:04x}", device.info.vendor_id, device.info.product_id);
                
                // Handle some basic standard requests
                if request_type & 0x80 != 0 { // IN direction
                    let response_data = match (request_type & 0x60, request) {
                        (0x00, 0x06) => { // GET_DESCRIPTOR
                            match (value >> 8) as u8 {
                                0x01 => { // Device descriptor
                                    vec![
                                        0x12, 0x01, // bLength, bDescriptorType
                                        0x00, 0x02, // bcdUSB (2.0)
                                        device.info.device_class, device.info.device_subclass, device.info.device_protocol, // bDeviceClass/SubClass/Protocol
                                        0x40, // bMaxPacketSize0
                                        (device.info.vendor_id & 0xFF) as u8, (device.info.vendor_id >> 8) as u8, // idVendor
                                        (device.info.product_id & 0xFF) as u8, (device.info.product_id >> 8) as u8, // idProduct
                                        (device.info.bcd_device & 0xFF) as u8, (device.info.bcd_device >> 8) as u8, // bcdDevice
                                        0x00, 0x00, 0x00, device.info.num_configurations // iManufacturer, iProduct, iSerialNumber, bNumConfigurations
                                    ]
                                }
                                0x02 => { // Configuration descriptor
                                    vec![
                                        0x09, 0x02, // bLength, bDescriptorType
                                        0x20, 0x00, // wTotalLength (32)
                                        0x01, // bNumInterfaces
                                        0x01, // bConfigurationValue
                                        0x00, // iConfiguration
                                        0x80, // bmAttributes
                                        0xFA, // bMaxPower (500mA)
                                        // Interface descriptor
                                        0x09, 0x04, // bLength, bDescriptorType
                                        0x00, // bInterfaceNumber
                                        0x00, // bAlternateSetting
                                        0x02, // bNumEndpoints
                                        0x08, // bInterfaceClass (Mass Storage)
                                        0x06, // bInterfaceSubClass (SCSI)
                                        0x50, // bInterfaceProtocol (Bulk-Only)
                                        0x00, // iInterface
                                        // Endpoint descriptors
                                        0x07, 0x05, 0x81, 0x02, 0x00, 0x02, 0x00, // EP1 IN
                                        0x07, 0x05, 0x02, 0x02, 0x00, 0x02, 0x00, // EP2 OUT
                                    ]
                                }
                                _ => vec![]
                            }
                        }
                        (0x00, 0x08) => { // GET_CONFIGURATION
                            vec![device.info.configuration_value]
                        }
                        _ => vec![]
                    };
                    
                    let actual_length = response_data.len().min(length as usize);
                    return Ok(TransferResponse {
                        seqnum: transfer.seqnum,
                        status: 0,
                        actual_length: actual_length as u32,
                        data: Some(response_data[..actual_length].to_vec()),
                        start_frame: 0,
                        number_of_packets: 0,
                        error_count: 0,
                    });
                } else { // OUT direction
                    // For OUT control transfers, just acknowledge
                    return Ok(TransferResponse {
                        seqnum: transfer.seqnum,
                        status: 0,
                        actual_length: transfer.transfer_length,
                        data: None,
                        start_frame: 0,
                        number_of_packets: 0,
                        error_count: 0,
                    });
                }
            }
        } else {
            // Bulk/Interrupt/Isoc transfers
            warn!("Non-control transfers not yet implemented for endpoint {}", transfer.endpoint);
        }
        
        // Default error response
        Ok(TransferResponse {
            seqnum: transfer.seqnum,
            status: -1,
            actual_length: 0,
            data: None,
            start_frame: 0,
            number_of_packets: 0,
            error_count: 1,
        })
    }
    
    fn unlink_transfer(&mut self, device_id: u32, seqnum: u32) -> Result<()> {
        debug!("Unlinking transfer: device={}, seq={}", device_id, seqnum);
        // TODO: Implement transfer cancellation
        Ok(())
    }
    
    fn release_device(&mut self, device_id: u32) -> Result<()> {
        info!("Releasing device: {}", device_id);
        
        if let Some(mut device) = self.devices.remove(&device_id) {
            unsafe {
                // Close pipes
                device.pipes.clear();
                
                // Close interfaces
                for (_, interface) in device.interfaces.drain() {
                    let _: () = msg_send![&interface, close];
                }
                
                // Close device
                let _: () = msg_send![&device.device, close];
            }
            
            // Remove from busid mapping
            self.busid_to_device_id.retain(|_, &mut id| id != device_id);
        }
        
        Ok(())
    }
}