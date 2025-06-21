use anyhow::{Result, anyhow};
use std::collections::HashMap;
use std::sync::Arc;
use tokio::sync::Mutex;
use tracing::{info, debug};

use crate::{BeU16, BeU32};
use crate::proto::*;
use crate::device_manager::{DeviceManager, DeviceInfo, TransferRequest, TransferResponse};

pub struct ImportedDeviceState {
    pub device_id: u32,
    pub info: DeviceInfo,
}

pub struct UsbManager {
    device_manager: Arc<DeviceManager>,
    imported_devices: Arc<Mutex<HashMap<String, ImportedDeviceState>>>, // busid -> device state
    device_id_to_busid: Arc<Mutex<HashMap<u32, String>>>, // device_id -> busid
    client_devid_to_device_id: Arc<Mutex<HashMap<u32, u32>>>, // client devid -> our device_id
}

impl UsbManager {
    pub fn new() -> Self {
        Self {
            device_manager: Arc::new(DeviceManager::new()),
            imported_devices: Arc::new(Mutex::new(HashMap::new())),
            device_id_to_busid: Arc::new(Mutex::new(HashMap::new())),
            client_devid_to_device_id: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    pub async fn scan_devices(&self) -> Result<()> {
        debug!("Scanning for USB devices");
        let _ = self.device_manager.scan_devices().await?;
        Ok(())
    }

    pub async fn get_device_list(&self) -> Result<Vec<UsbDevice>> {
        let device_infos = self.device_manager.scan_devices().await?;
        let mut device_list = Vec::new();

        for info in device_infos {
            let mut device = UsbDevice {
                path: [0; 256],
                busid: [0; 32],
                busnum: BeU32::new(info.bus_number),
                devnum: BeU32::new(info.device_number),
                speed: BeU32::new(info.speed),
                id_vendor: BeU16::new(info.vendor_id),
                id_product: BeU16::new(info.product_id),
                bcd_device: BeU16::new(info.bcd_device),
                b_device_class: info.device_class,
                b_device_sub_class: info.device_subclass,
                b_device_protocol: info.device_protocol,
                b_configuration_value: info.configuration_value,
                b_num_configurations: info.num_configurations,
                b_num_interfaces: info.interfaces.len() as u8,
            };

            // Copy path and busid
            let path_bytes = info.path.as_bytes();
            let busid_bytes = info.busid.as_bytes();
            device.path[..path_bytes.len().min(256)].copy_from_slice(&path_bytes[..path_bytes.len().min(256)]);
            device.busid[..busid_bytes.len().min(32)].copy_from_slice(&busid_bytes[..busid_bytes.len().min(32)]);

            device_list.push(device);
        }

        Ok(device_list)
    }

    pub async fn get_interfaces(&self, busid: &str) -> Result<Vec<UsbInterface>> {
        // First check if device is imported
        let imported = self.imported_devices.lock().await;
        if let Some(device_state) = imported.get(busid) {
            let interfaces: Vec<UsbInterface> = device_state.info.interfaces.iter().map(|iface| {
                UsbInterface {
                    b_interface_class: iface.interface_class,
                    b_interface_sub_class: iface.interface_subclass,
                    b_interface_protocol: iface.interface_protocol,
                    padding: 0,
                }
            }).collect();
            return Ok(interfaces);
        }
        drop(imported);

        // Otherwise scan for the device
        let device_infos = self.device_manager.scan_devices().await?;
        let info = device_infos.into_iter()
            .find(|d| d.busid == busid)
            .ok_or_else(|| anyhow!("Device not found: {}", busid))?;

        let interfaces: Vec<UsbInterface> = info.interfaces.iter().map(|iface| {
            UsbInterface {
                b_interface_class: iface.interface_class,
                b_interface_sub_class: iface.interface_subclass,
                b_interface_protocol: iface.interface_protocol,
                padding: 0,
            }
        }).collect();
        
        Ok(interfaces)
    }

    pub async fn import_device(&self, busid: &str) -> Result<UsbDevice> {
        // Check if already imported
        let imported = self.imported_devices.lock().await;
        if let Some(device_state) = imported.get(busid) {
            let info = &device_state.info;
            let mut device = UsbDevice {
                path: [0; 256],
                busid: [0; 32],
                busnum: BeU32::new(info.bus_number),
                devnum: BeU32::new(info.device_number),
                speed: BeU32::new(info.speed),
                id_vendor: BeU16::new(info.vendor_id),
                id_product: BeU16::new(info.product_id),
                bcd_device: BeU16::new(info.bcd_device),
                b_device_class: info.device_class,
                b_device_sub_class: info.device_subclass,
                b_device_protocol: info.device_protocol,
                b_configuration_value: info.configuration_value,
                b_num_configurations: info.num_configurations,
                b_num_interfaces: info.interfaces.len() as u8,
            };

            let path_bytes = info.path.as_bytes();
            let busid_bytes = busid.as_bytes();
            device.path[..path_bytes.len().min(256)].copy_from_slice(&path_bytes[..path_bytes.len().min(256)]);
            device.busid[..busid_bytes.len().min(32)].copy_from_slice(&busid_bytes[..busid_bytes.len().min(32)]);

            return Ok(device);
        }
        drop(imported);

        // Import the device
        let imported_device = self.device_manager.import_device(busid).await?;
        info!("Successfully imported device: {} with ID {}", busid, imported_device.device_id);

        // Store the imported device state
        let mut imported = self.imported_devices.lock().await;
        let mut id_to_busid = self.device_id_to_busid.lock().await;
        
        // Calculate client device ID (busnum << 16 | devnum)
        let client_devid = (imported_device.info.bus_number << 16) | imported_device.info.device_number;
        
        imported.insert(busid.to_string(), ImportedDeviceState {
            device_id: imported_device.device_id,
            info: imported_device.info.clone(),
        });
        id_to_busid.insert(imported_device.device_id, busid.to_string());
        
        // Store the client devid mapping
        let mut devid_map = self.client_devid_to_device_id.lock().await;
        devid_map.insert(client_devid, imported_device.device_id);
        debug!("Mapped client devid {} to internal device_id {}", client_devid, imported_device.device_id);

        // Return device info
        let info = &imported_device.info;
        let mut device = UsbDevice {
            path: [0; 256],
            busid: [0; 32],
            busnum: BeU32::new(info.bus_number),
            devnum: BeU32::new(info.device_number),
            speed: BeU32::new(info.speed),
            id_vendor: BeU16::new(info.vendor_id),
            id_product: BeU16::new(info.product_id),
            bcd_device: BeU16::new(info.bcd_device),
            b_device_class: info.device_class,
            b_device_sub_class: info.device_subclass,
            b_device_protocol: info.device_protocol,
            b_configuration_value: info.configuration_value,
            b_num_configurations: info.num_configurations,
            b_num_interfaces: info.interfaces.len() as u8,
        };

        let path_bytes = info.path.as_bytes();
        let busid_bytes = busid.as_bytes();
        device.path[..path_bytes.len().min(256)].copy_from_slice(&path_bytes[..path_bytes.len().min(256)]);
        device.busid[..busid_bytes.len().min(32)].copy_from_slice(&busid_bytes[..busid_bytes.len().min(32)]);

        Ok(device)
    }

    pub async fn submit_transfer(&self, devid: u32, seqnum: u32, direction: u32, endpoint: u32,
                                 transfer_flags: u32, transfer_length: u32, setup: Option<[u8; 8]>,
                                 data: Option<Vec<u8>>, start_frame: u32, number_of_packets: u32,
                                 interval: u32) -> Result<TransferResponse> {
        // Map client devid to our internal device_id
        let devid_map = self.client_devid_to_device_id.lock().await;
        let device_id = *devid_map.get(&devid)
            .ok_or_else(|| anyhow!("Device ID not found: {}", devid))?;
        drop(devid_map);

        // Submit the transfer
        let transfer_req = TransferRequest {
            seqnum,
            direction,
            endpoint,
            transfer_flags,
            transfer_length,
            setup,
            data,
            start_frame,
            number_of_packets,
            interval,
        };

        self.device_manager.submit_transfer(device_id, transfer_req).await
    }

    pub async fn unlink_transfer(&self, devid: u32, seqnum: u32) -> Result<()> {
        // Map client devid to our internal device_id
        let devid_map = self.client_devid_to_device_id.lock().await;
        let device_id = *devid_map.get(&devid)
            .ok_or_else(|| anyhow!("Device ID not found: {}", devid))?;
        drop(devid_map);

        self.device_manager.unlink_transfer(device_id, seqnum).await
    }

    pub async fn release_device(&self, busid: &str) -> Result<()> {
        let mut imported = self.imported_devices.lock().await;
        if let Some(device_state) = imported.remove(busid) {
            let mut id_to_busid = self.device_id_to_busid.lock().await;
            id_to_busid.remove(&device_state.device_id);
            drop(id_to_busid);
            drop(imported);

            self.device_manager.release_device(device_state.device_id).await?;
            info!("Released device: {}", busid);
        }
        Ok(())
    }
}