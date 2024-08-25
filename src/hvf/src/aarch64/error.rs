use super::bindings::{
    hv_return_t, HV_BAD_ARGUMENT, HV_BUSY, HV_DENIED, HV_ERROR, HV_ILLEGAL_GUEST_STATE,
    HV_NO_DEVICE, HV_NO_RESOURCES, HV_SUCCESS, HV_UNSUPPORTED,
};

#[derive(thiserror::Error, Debug)]
#[must_use]
pub enum HvfError {
    #[error("error")]
    Error,
    #[error("busy")]
    Busy,
    #[error("bad argument")]
    BadArgument,
    #[error("illegal guest state")]
    IllegalGuestState,
    #[error("no resources")]
    NoResources,
    #[error("no device")]
    NoDevice,
    #[error("denied")]
    Denied,
    #[error("unsupported")]
    Unsupported,
    #[error("unknown")]
    Unknown,
}

impl HvfError {
    pub(crate) fn result(ret: hv_return_t) -> Result<(), Self> {
        match ret {
            HV_SUCCESS => Ok(()),
            _ => Err(Self::from_i32(ret)),
        }
    }

    // results in much better codegen for the common case
    #[cold]
    fn from_i32(ret: hv_return_t) -> Self {
        match ret {
            HV_ERROR => HvfError::Error,
            HV_BUSY => HvfError::Busy,
            HV_BAD_ARGUMENT => HvfError::BadArgument,
            HV_ILLEGAL_GUEST_STATE => HvfError::IllegalGuestState,
            HV_NO_RESOURCES => HvfError::NoResources,
            HV_NO_DEVICE => HvfError::NoDevice,
            HV_DENIED => HvfError::Denied,
            HV_UNSUPPORTED => HvfError::Unsupported,
            _ => HvfError::Unknown,
        }
    }
}

#[derive(thiserror::Error, Debug)]
pub enum Error {
    #[error("memory map: {0}")]
    MemoryMap(HvfError),
    #[error("memory unmap: {0}")]
    MemoryUnmap(HvfError),
    #[error("memory protect: {0}")]
    MemoryProtect(HvfError),
    #[error("vcpu create: {0}")]
    VcpuCreate(HvfError),
    #[error("vcpu initial registers: {0}")]
    VcpuInitialRegisters(HvfError),
    #[error("vcpu read register: {0}")]
    VcpuReadRegister(HvfError),
    #[error("vcpu read system register: {0}")]
    VcpuReadSystemRegister(HvfError),
    #[error("vcpu request exit: {0}")]
    VcpuRequestExit(HvfError),
    #[error("vcpu run: {0}")]
    VcpuRun(HvfError),
    #[error("vcpu set pending irq: {0}")]
    VcpuSetPendingIrq(HvfError),
    #[error("vcpu set register: {0}")]
    VcpuSetRegister(HvfError),
    #[error("vcpu set system register: {0}")]
    VcpuSetSystemRegister(HvfError),
    #[error("vcpu set vtimer mask: {0}")]
    VcpuSetVtimerMask(HvfError),
    #[error("vm config set ipa size: {0}")]
    VmConfigSetIpaSize(HvfError),
    #[error("vm config enable nested virt: {0}")]
    VmConfigEnableNestedVirt(HvfError),
    #[error("vm create: {0}")]
    VmCreate(HvfError),
    #[error("vm allocate: {0}")]
    VmAllocate(HvfError),
    #[error("vm deallocate: {0}")]
    VmDeallocate(HvfError),
    #[error("host CPU doesn't support assigning {0} bits of VM memory")]
    VmConfigIpaSizeLimit(u32),
    #[error("vm config get default ipa size: {0}")]
    VmConfigGetDefaultIpaSize(HvfError),
    #[error("vm config get max ipa size: {0}")]
    VmConfigGetMaxIpaSize(HvfError),
    #[error("guest memory map")]
    GetGuestMemory,
    #[error("vm config get el2 supported: {0}")]
    VmConfigGetEl2Supported(HvfError),
    #[error("vm config set el2 enabled: {0}")]
    VmConfigSetEl2Enabled(HvfError),
    #[error("gic config create")]
    GicConfigCreate,
    #[error("gic get distributor size: {0}")]
    GicGetDistributorSize(HvfError),
    #[error("gic get redistributor size: {0}")]
    GicGetRedistributorSize(HvfError),
    #[error("gic config set distributor base: {0}")]
    GicConfigSetDistributorBase(HvfError),
    #[error("gic config set redistributor base: {0}")]
    GicConfigSetRedistributorBase(HvfError),
    #[error("gic create: {0}")]
    GicCreate(HvfError),
    #[error("gic get intid: {0}")]
    GicGetIntid(HvfError),
    #[error("gic set intid: {0}")]
    GicAssertSpi(HvfError),
    #[error("gic get spi range: {0}")]
    GicGetSpiRange(HvfError),
    #[error("gic config set msi size: {0}")]
    GicConfigSetMsiSize(HvfError),
    #[error("translate virtual address")]
    TranslateVirtualAddress,
    #[error("translate virtual address: pa not supported")]
    TranslateVirtualAddressPaNotSupported,
}
