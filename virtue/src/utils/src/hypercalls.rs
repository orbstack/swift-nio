use bitflags::bitflags;

pub const PSCI_VERSION: u32 = 0x8400_0000;
pub const PSCI_VERSION_1_0: u32 = psci_version(1, 0);
const fn psci_version(major: u16, minor: u16) -> u32 {
    ((major as u32) << 16) | minor as u32
}

pub const PSCI_MIGRATE_TYPE: u32 = 0x8400_0006;
pub const PSCI_POWER_OFF: u32 = 0x8400_0008;
pub const PSCI_RESET: u32 = 0x8400_0009;
pub const PSCI_CPU_ON: u32 = 0xc400_0003;

pub const PSCI_MIGRATE_TYPE_MP: u32 = 2;

pub const PSCI_1_0_FEATURES: u32 = 0x8400000a;

pub const SMCCC_VERSION: u32 = 0x80000000;
pub const SMCCC_ARCH_FEATURES: u32 = 0x80000001;
pub const SMCCC_TRNG_VERSION: u32 = 0x84000050;

// same encoding
pub const SMCCC_VERSION_1_1: u32 = psci_version(1, 1);

pub const VENDOR_UID: u32 = 0x8600_ff01;
pub const KVM_UID: [u32; 4] = [0xb66fb428, 0xe911c52e, 0x564bcaa9, 0x743a004d];

pub const KVM_FEATURES: u32 = 0x86000000;
bitflags! {
    pub struct KvmFeatures: u32 {
        const FEATURES = 1 << 0;
        const PTP = 1 << 1;
    }
}

pub const KVM_PTP: u32 = 0x8600_0001;
pub const KVM_PTP_VIRT_COUNTER: u64 = 0;
pub const KVM_PTP_PHYS_COUNTER: u64 = 1;

// SMCCC: (fast call, 64-bit, vendor hyp owner, 0xe000 +ID)
const fn orbvm_hvc_id(id: u32) -> u32 {
    0xc600_e000 + id
}

// kernel code uses more obscure constant names because code may become public

// kernel: ORBVM_FEATURES
pub const ORBVM_FEATURES: u32 = orbvm_hvc_id(1);
// kernel: ORBVM_WFK
pub const ORBVM_PVLOCK_WFK: u32 = orbvm_hvc_id(2);
// kernel: ORBVM_KICK
pub const ORBVM_PVLOCK_KICK: u32 = orbvm_hvc_id(3);
// kernel: ORBVM_IOR
pub const ORBVM_IO_REQUEST: u32 = orbvm_hvc_id(4);
// kernel: ORBVM_SET_PVG
pub const ORBVM_PVGIC_SET_STATE: u32 = orbvm_hvc_id(5);
// kernel: ORBVM_SET_REG
pub const ORBVM_SET_ACTLR_EL1: u32 = orbvm_hvc_id(6);
// kernel: ORBVM_MMIO_WRITE32
pub const ORBVM_MMIO_WRITE32: u32 = orbvm_hvc_id(7);

bitflags! {
    pub struct OrbvmFeatures: u64 {
        // to test disabling a feature, just comment it here
        // kernel: ORBVM_FEAT_*
        const FS = 1 << 0;
        const BLK = 1 << 1;
        const MMIO = 1 << 2;
        const CONSOLE = 1 << 3;
    }
}

// HVC I/O device IDs
pub const HVC_DEVICE_VIRTIOFS_ROOT: usize = 0;
pub const HVC_DEVICE_VIRTIOFS_ROSETTA: usize = 1;
pub const HVC_DEVICE_BLOCK_START: usize = 2000;
pub const HVC_DEVICE_CONSOLE_START: usize = 3000;

pub const ORBVM_CONSOLE_REQ_WRITE: u16 = 0;

pub const SMCCC_RET_SUCCESS: u64 = 0;
pub const SMCCC_RET_NOT_SUPPORTED: u64 = (-1i64) as u64;
pub const SMCCC_RET_NOT_REQUIRED: u64 = (-2i64) as u64;
pub const SMCCC_RET_INVALID_PARAMETER: u64 = (-3i64) as u64;
