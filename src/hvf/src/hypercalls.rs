pub const PSCI_VERSION: u32 = 0x8400_0000;
pub const PSCI_MIGRATE_TYPE: u32 = 0x8400_0006;
pub const PSCI_POWER_OFF: u32 = 0x8400_0008;
pub const PSCI_RESET: u32 = 0x8400_0009;
pub const PSCI_CPU_ON: u32 = 0xc400_0003;

// SMCCC: (fast call, 64-bit, vendor hyp owner, ID)
const fn orbvm_hvc_id(id: u32) -> u32 {
    0xc600_e000 + id
}

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
// kernel: ORBVM_MEM_UNREPORT
pub const ORBVM_MADVISE_REUSE: u32 = orbvm_hvc_id(7);
