use bytemuck::{Pod, Zeroable};
use crate::endian::{BeU16, BeU32};

// Protocol version
pub const USBIP_VERSION: u16 = 0x0111;

// Command codes
pub const OP_REQ_DEVLIST: u16 = 0x8005;
pub const OP_REP_DEVLIST: u16 = 0x0005;
pub const OP_REQ_IMPORT: u16 = 0x8003;
pub const OP_REP_IMPORT: u16 = 0x0003;

pub const USBIP_CMD_SUBMIT: u32 = 0x00000001;
pub const USBIP_RET_SUBMIT: u32 = 0x00000003;
pub const USBIP_CMD_UNLINK: u32 = 0x00000002;
pub const USBIP_RET_UNLINK: u32 = 0x00000004;

// URB directions
pub const USBIP_DIR_OUT: u32 = 0;
pub const USBIP_DIR_IN: u32 = 1;

// Status codes
pub const ST_OK: u32 = 0;
pub const ST_ERROR: u32 = 1;

// USB speeds
pub const USB_SPEED_UNKNOWN: u32 = 0;
pub const USB_SPEED_LOW: u32 = 1;
pub const USB_SPEED_FULL: u32 = 2;
pub const USB_SPEED_HIGH: u32 = 3;
pub const USB_SPEED_WIRELESS: u32 = 4;
pub const USB_SPEED_SUPER: u32 = 5;
pub const USB_SPEED_SUPER_PLUS: u32 = 6;

// Common header for all packets
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpHeader {
    pub version: BeU16,
    pub command: BeU16,
    pub status: BeU32,
}

// OP_REQ_DEVLIST request
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct OpReqDevlist {
    pub header: UsbIpHeader,
}

// OP_REP_DEVLIST reply header
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct OpRepDevlistHeader {
    pub header: UsbIpHeader,
    pub num_exported_device: BeU32,
}

// USB device descriptor in OP_REP_DEVLIST
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbDevice {
    pub path: [u8; 256],
    pub busid: [u8; 32],
    pub busnum: BeU32,
    pub devnum: BeU32,
    pub speed: BeU32,
    pub id_vendor: BeU16,
    pub id_product: BeU16,
    pub bcd_device: BeU16,
    pub b_device_class: u8,
    pub b_device_sub_class: u8,
    pub b_device_protocol: u8,
    pub b_configuration_value: u8,
    pub b_num_configurations: u8,
    pub b_num_interfaces: u8,
}

// USB interface descriptor in OP_REP_DEVLIST
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbInterface {
    pub b_interface_class: u8,
    pub b_interface_sub_class: u8,
    pub b_interface_protocol: u8,
    pub padding: u8,
}

// OP_REQ_IMPORT request
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct OpReqImport {
    pub header: UsbIpHeader,
    pub busid: [u8; 32],
}

// OP_REP_IMPORT reply
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct OpRepImport {
    pub header: UsbIpHeader,
    pub device: UsbDevice,
}

// Basic header for CMD/RET packets
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpHeaderBasic {
    pub command: BeU32,
    pub seqnum: BeU32,
    pub devid: BeU32,
    pub direction: BeU32,
    pub ep: BeU32,
}

// USBIP_CMD_SUBMIT packet
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpCmdSubmit {
    pub header: UsbIpHeaderBasic,
    pub transfer_flags: BeU32,
    pub transfer_buffer_length: BeU32,
    pub start_frame: BeU32,
    pub number_of_packets: BeU32,
    pub interval: BeU32,
    pub setup: [u8; 8],
}

// USBIP_RET_SUBMIT packet
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpRetSubmit {
    pub header: UsbIpHeaderBasic,
    pub status: BeU32,
    pub actual_length: BeU32,
    pub start_frame: BeU32,
    pub number_of_packets: BeU32,
    pub error_count: BeU32,
}

// USBIP_CMD_UNLINK packet
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpCmdUnlink {
    pub header: UsbIpHeaderBasic,
    pub unlink_seqnum: BeU32,
    pub padding: [u8; 24],
}

// USBIP_RET_UNLINK packet
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpRetUnlink {
    pub header: UsbIpHeaderBasic,
    pub status: BeU32,
    pub padding: [u8; 24],
}

// ISO packet descriptor
#[derive(Debug, Clone, Copy, Pod, Zeroable)]
#[repr(C)]
pub struct UsbIpIsoPacketDescriptor {
    pub offset: BeU32,
    pub length: BeU32,
    pub actual_length: BeU32,
    pub status: BeU32,
}

// Helper functions
impl UsbIpHeader {
    pub fn new(command: u16, status: u32) -> Self {
        Self {
            version: BeU16::new(USBIP_VERSION),
            command: BeU16::new(command),
            status: BeU32::new(status),
        }
    }
}

impl UsbIpHeaderBasic {
    pub fn new(command: u32, seqnum: u32, devid: u32, direction: u32, ep: u32) -> Self {
        Self {
            command: BeU32::new(command),
            seqnum: BeU32::new(seqnum),
            devid: BeU32::new(devid),
            direction: BeU32::new(direction),
            ep: BeU32::new(ep),
        }
    }
}

// USB transfer flags
pub const URB_SHORT_NOT_OK: u32 = 0x00000001;
pub const URB_ISO_ASAP: u32 = 0x00000002;
pub const URB_NO_TRANSFER_DMA_MAP: u32 = 0x00000004;
pub const URB_ZERO_PACKET: u32 = 0x00000040;
pub const URB_NO_INTERRUPT: u32 = 0x00000080;
pub const URB_FREE_BUFFER: u32 = 0x00000100;
pub const URB_DIR_IN: u32 = 0x00000200;
pub const URB_DIR_OUT: u32 = 0x00000000;
pub const URB_DIR_MASK: u32 = 0x00000200;

// USB endpoint types
pub const USB_ENDPOINT_XFER_CONTROL: u8 = 0;
pub const USB_ENDPOINT_XFER_ISOC: u8 = 1;
pub const USB_ENDPOINT_XFER_BULK: u8 = 2;
pub const USB_ENDPOINT_XFER_INT: u8 = 3;
pub const USB_ENDPOINT_XFER_MASK: u8 = 3;

// USB class codes
pub const USB_CLASS_PER_INTERFACE: u8 = 0;
pub const USB_CLASS_AUDIO: u8 = 1;
pub const USB_CLASS_COMM: u8 = 2;
pub const USB_CLASS_HID: u8 = 3;
pub const USB_CLASS_PHYSICAL: u8 = 5;
pub const USB_CLASS_STILL_IMAGE: u8 = 6;
pub const USB_CLASS_PRINTER: u8 = 7;
pub const USB_CLASS_MASS_STORAGE: u8 = 8;
pub const USB_CLASS_HUB: u8 = 9;
pub const USB_CLASS_CDC_DATA: u8 = 10;
pub const USB_CLASS_CSCID: u8 = 11;
pub const USB_CLASS_CONTENT_SEC: u8 = 13;
pub const USB_CLASS_VIDEO: u8 = 14;
pub const USB_CLASS_WIRELESS_CONTROLLER: u8 = 224;
pub const USB_CLASS_MISC: u8 = 239;
pub const USB_CLASS_APP_SPEC: u8 = 254;
pub const USB_CLASS_VENDOR_SPEC: u8 = 255;