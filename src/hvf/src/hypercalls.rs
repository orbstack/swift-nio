pub const PSCI_VERSION: u32 = 0x8400_0000;
pub const PSCI_MIGRATE_TYPE: u32 = 0x8400_0006;
pub const PSCI_POWER_OFF: u32 = 0x8400_0008;
pub const PSCI_RESET: u32 = 0x8400_0009;
pub const PSCI_CPU_ON: u32 = 0xc400_0003;

// SMCCC: (fast call, 64-bit, standard owner, ID)
pub const RSVM_FEATURES: u32 = 0xc400_0029;
pub const RSVM_IO_REQ: u32 = 0xc400_002a;
pub const RSVM_PVGIC_SET_ADDR: u32 = 0xc400_002b;
pub const RSVM_SET_ACTLR_EL1: u32 = 0xc400_002c;

pub const VZF_PVLOCK_WAIT: u32 = 0xc300_0005;
pub const VZF_PVLOCK_KICK: u32 = 0xc300_0006;
