use anyhow::{Result, anyhow};
use tokio::net::{TcpListener, TcpStream};
use tokio::io::{AsyncReadExt, AsyncWriteExt};
use tracing::{info, debug, error};
use tracing_subscriber;
use std::sync::Arc;
use bytemuck;

use usbip::*;
use usbip::usb::UsbManager;

struct UsbIpServer {
    usb_manager: Arc<UsbManager>,
}

impl UsbIpServer {
    fn new() -> Self {
        Self {
            usb_manager: Arc::new(UsbManager::new()),
        }
    }

    async fn handle_client(&self, mut stream: TcpStream) -> Result<()> {
        let peer_addr = stream.peer_addr()?;
        info!("New client connected from: {}", peer_addr);

        loop {
            let mut header_buf = [0u8; std::mem::size_of::<UsbIpHeader>()];
            
            info!("Waiting for next command from client...");
            
            match stream.read_exact(&mut header_buf).await {
                Ok(_) => {},
                Err(e) if e.kind() == std::io::ErrorKind::UnexpectedEof => {
                    info!("Client {} disconnected", peer_addr);
                    break;
                }
                Err(e) => return Err(e.into()),
            }

            // Check if this might be a basic header (SUBMIT/UNLINK)
            let first_u32 = u32::from_be_bytes([header_buf[0], header_buf[1], header_buf[2], header_buf[3]]);
            info!("First 4 bytes as u32: 0x{:08x}", first_u32);
            
            let header: &UsbIpHeader = bytemuck::from_bytes(&header_buf);
            let command = header.command.get();
            let version = header.version.get();

            info!("Received command: 0x{:04x}, version: 0x{:04x}", command, version);

            match command {
                OP_REQ_DEVLIST => {
                    self.handle_devlist(&mut stream).await?;
                }
                OP_REQ_IMPORT => {
                    self.handle_import(&mut stream, header_buf).await?;
                    info!("Import completed, continuing to read commands...");
                }
                _ => {
                    info!("Unknown command 0x{:04x}, checking if it's a basic header", command);
                    // For SUBMIT/UNLINK, we need to read the basic header instead
                    let mut basic_header_buf = [0u8; std::mem::size_of::<UsbIpHeaderBasic>()];
                    basic_header_buf[..header_buf.len()].copy_from_slice(&header_buf);
                    
                    // Read the rest of the basic header
                    stream.read_exact(&mut basic_header_buf[header_buf.len()..]).await?;
                    
                    let basic_header: &UsbIpHeaderBasic = bytemuck::from_bytes(&basic_header_buf);
                    let cmd = basic_header.command.get();
                    
                    info!("Basic header command: 0x{:08x}, USBIP_CMD_SUBMIT=0x{:08x}, USBIP_CMD_UNLINK=0x{:08x}", 
                           cmd, USBIP_CMD_SUBMIT, USBIP_CMD_UNLINK);
                    match cmd {
                        USBIP_CMD_SUBMIT => {
                            info!("Matched SUBMIT command");
                            self.handle_submit(&mut stream, basic_header_buf).await?;
                        }
                        USBIP_CMD_UNLINK => {
                            info!("Matched UNLINK command");
                            self.handle_unlink(&mut stream, basic_header_buf).await?;
                        }
                        _ => {
                            error!("Unknown command: 0x{:08x}", cmd);
                            return Err(anyhow!("Unknown command"));
                        }
                    }
                }
            }
        }

        Ok(())
    }

    async fn handle_devlist(&self, stream: &mut TcpStream) -> Result<()> {
        debug!("Handling DEVLIST request");

        // Get device list from USB manager
        let devices = self.usb_manager.get_device_list().await?;
        
        // Send reply header
        let reply_header = OpRepDevlistHeader {
            header: UsbIpHeader::new(OP_REP_DEVLIST, ST_OK),
            num_exported_device: BeU32::new(devices.len() as u32),
        };
        
        stream.write_all(bytemuck::bytes_of(&reply_header)).await?;
        
        // Send each device
        for device in &devices {
            stream.write_all(bytemuck::bytes_of(device)).await?;
            
            // Get and send interfaces for this device
            let busid = std::str::from_utf8(&device.busid)
                .unwrap_or("")
                .trim_end_matches('\0');
            
            if let Ok(interfaces) = self.usb_manager.get_interfaces(busid).await {
                for interface in interfaces {
                    stream.write_all(bytemuck::bytes_of(&interface)).await?;
                }
            }
        }
        
        info!("Sent {} devices in DEVLIST response", devices.len());
        Ok(())
    }

    async fn handle_import(&self, stream: &mut TcpStream, header_buf: [u8; 8]) -> Result<()> {
        debug!("Handling IMPORT request");
        
        // Read the rest of the import request
        let mut import_buf = [0u8; std::mem::size_of::<OpReqImport>()];
        import_buf[..header_buf.len()].copy_from_slice(&header_buf);
        stream.read_exact(&mut import_buf[header_buf.len()..]).await?;
        
        let import_req: &OpReqImport = bytemuck::from_bytes(&import_buf);
        let busid = std::str::from_utf8(&import_req.busid)
            .unwrap_or("")
            .trim_end_matches('\0');
        
        info!("Import request for device: {}", busid);
        
        match self.usb_manager.import_device(busid).await {
            Ok(device) => {
                let reply = OpRepImport {
                    header: UsbIpHeader::new(OP_REP_IMPORT, ST_OK),
                    device,
                };
                stream.write_all(bytemuck::bytes_of(&reply)).await?;
                info!("Successfully imported device: {}", busid);
            }
            Err(e) => {
                error!("Failed to import device {}: {}", busid, e);
                let reply = OpRepImport {
                    header: UsbIpHeader::new(OP_REP_IMPORT, ST_ERROR),
                    device: UsbDevice {
                        path: [0; 256],
                        busid: [0; 32],
                        busnum: BeU32::new(0),
                        devnum: BeU32::new(0),
                        speed: BeU32::new(0),
                        id_vendor: BeU16::new(0),
                        id_product: BeU16::new(0),
                        bcd_device: BeU16::new(0),
                        b_device_class: 0,
                        b_device_sub_class: 0,
                        b_device_protocol: 0,
                        b_configuration_value: 0,
                        b_num_configurations: 0,
                        b_num_interfaces: 0,
                    },
                };
                stream.write_all(bytemuck::bytes_of(&reply)).await?;
            }
        }
        
        Ok(())
    }

    async fn handle_submit(&self, stream: &mut TcpStream, header_buf: [u8; 20]) -> Result<()> {
        info!("Handling SUBMIT request");
        let header: &UsbIpHeaderBasic = bytemuck::from_bytes(&header_buf);
        info!("SUBMIT header - seqnum: {}, devid: 0x{:x}, direction: {}, ep: {}", 
               header.seqnum.get(), header.devid.get(), header.direction.get(), header.ep.get());
        
        // Read the rest of the submit request
        let mut submit_buf = [0u8; std::mem::size_of::<UsbIpCmdSubmit>()];
        submit_buf[..header_buf.len()].copy_from_slice(&header_buf);
        stream.read_exact(&mut submit_buf[header_buf.len()..]).await?;
        
        let submit_cmd: &UsbIpCmdSubmit = bytemuck::from_bytes(&submit_buf);
        let seqnum = submit_cmd.header.seqnum.get();
        let devid = submit_cmd.header.devid.get();
        let direction = submit_cmd.header.direction.get();
        let ep = submit_cmd.header.ep.get();
        let transfer_length = submit_cmd.transfer_buffer_length.get();
        let transfer_flags = submit_cmd.transfer_flags.get();
        let start_frame = submit_cmd.start_frame.get();
        let number_of_packets = submit_cmd.number_of_packets.get();
        let interval = submit_cmd.interval.get();
        
        info!("SUBMIT: seq={}, dev=0x{:x}, dir={}, ep={}, flags=0x{:x}, len={}", 
               seqnum, devid, direction, ep, transfer_flags, transfer_length);
        
        // Read transfer data if OUT direction
        let transfer_data = if direction == USBIP_DIR_OUT && transfer_length > 0 {
            let mut data = vec![0u8; transfer_length as usize];
            stream.read_exact(&mut data).await?;
            Some(data)
        } else {
            None
        };
        
        // Extract setup packet for control transfers
        let setup = if ep == 0 {
            let s = submit_cmd.setup;
            info!("Control transfer setup: {:02x} {:02x} {:02x} {:02x} {:02x} {:02x} {:02x} {:02x}",
                  s[0], s[1], s[2], s[3], s[4], s[5], s[6], s[7]);
            Some(s)
        } else {
            None
        };
        
        // Submit the transfer to the USB device
        match self.usb_manager.submit_transfer(
            devid, seqnum, direction, ep,
            transfer_flags, transfer_length, setup,
            transfer_data, start_frame, number_of_packets,
            interval
        ).await {
            Ok(response) => {
                info!("Transfer completed - status: {}, actual_length: {}", response.status, response.actual_length);
                let ret_submit = UsbIpRetSubmit {
                    header: UsbIpHeaderBasic::new(USBIP_RET_SUBMIT, seqnum, 0, direction, ep),
                    status: BeU32::new(response.status as u32),
                    actual_length: BeU32::new(response.actual_length),
                    start_frame: BeU32::new(response.start_frame),
                    number_of_packets: BeU32::new(response.number_of_packets),
                    error_count: BeU32::new(response.error_count),
                };
                
                let ret_bytes = bytemuck::bytes_of(&ret_submit);
                info!("Sending RET_SUBMIT ({} bytes): {:02x?}", ret_bytes.len(), ret_bytes);
                stream.write_all(ret_bytes).await?;
                
                // Send data if IN direction and we have data
                if let Some(data) = response.data {
                    info!("Sending {} bytes of response data: {:02x?}", data.len(), data);
                    stream.write_all(&data).await?;
                }
                stream.flush().await?;
            }
            Err(e) => {
                error!("Transfer failed: {}", e);
                let ret_submit = UsbIpRetSubmit {
                    header: UsbIpHeaderBasic::new(USBIP_RET_SUBMIT, seqnum, 0, direction, ep),
                    status: BeU32::new(0xffffffff), // Error
                    actual_length: BeU32::new(0),
                    start_frame: BeU32::new(0),
                    number_of_packets: BeU32::new(0),
                    error_count: BeU32::new(1),
                };
                
                stream.write_all(bytemuck::bytes_of(&ret_submit)).await?;
                stream.flush().await?;
            }
        }
        
        Ok(())
    }

    async fn handle_unlink(&self, stream: &mut TcpStream, header_buf: [u8; 20]) -> Result<()> {
        debug!("Handling UNLINK request");
        
        // Read the rest of the unlink request
        let mut unlink_buf = [0u8; std::mem::size_of::<UsbIpCmdUnlink>()];
        unlink_buf[..header_buf.len()].copy_from_slice(&header_buf);
        stream.read_exact(&mut unlink_buf[header_buf.len()..]).await?;
        
        let unlink_cmd: &UsbIpCmdUnlink = bytemuck::from_bytes(&unlink_buf);
        let seqnum = unlink_cmd.header.seqnum.get();
        let devid = unlink_cmd.header.devid.get();
        let unlink_seqnum = unlink_cmd.unlink_seqnum.get();
        
        debug!("UNLINK: seq={}, dev={}, unlink_seq={}", seqnum, devid, unlink_seqnum);
        
        // Try to unlink the transfer
        let status = match self.usb_manager.unlink_transfer(devid, unlink_seqnum).await {
            Ok(_) => 0, // Success
            Err(e) => {
                error!("Failed to unlink transfer: {}", e);
                0xffffffff // Error
            }
        };
        
        let ret_unlink = UsbIpRetUnlink {
            header: UsbIpHeaderBasic::new(USBIP_RET_UNLINK, seqnum, 0, 0, 0),
            status: BeU32::new(status),
            padding: [0; 24],
        };
        
        stream.write_all(bytemuck::bytes_of(&ret_unlink)).await?;
        
        Ok(())
    }

    async fn run(&self, addr: &str) -> Result<()> {
        let listener = TcpListener::bind(addr).await?;
        info!("USB/IP server listening on {}", addr);
        
        // Start device scanning in background
        let usb_manager = self.usb_manager.clone();
        tokio::spawn(async move {
            if let Err(e) = usb_manager.scan_devices().await {
                error!("Failed to scan devices: {}", e);
            }
        });
        
        loop {
            let (stream, _) = listener.accept().await?;
            let server = self.clone();
            
            tokio::spawn(async move {
                if let Err(e) = server.handle_client(stream).await {
                    error!("Error handling client: {}", e);
                }
            });
        }
    }
}

impl Clone for UsbIpServer {
    fn clone(&self) -> Self {
        Self {
            usb_manager: self.usb_manager.clone(),
        }
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    // Initialize tracing with env filter
    let filter = tracing_subscriber::EnvFilter::try_from_default_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info"));
    
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .init();
    
    let server = UsbIpServer::new();
    let addr = "0.0.0.0:3240"; // Standard USB/IP port
    
    server.run(addr).await
}