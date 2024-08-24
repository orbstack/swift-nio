use mach2::kern_return::kern_return_t;

#[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
#[must_use]
pub enum KernReturn {
    Success,
    InvalidAddress,
    ProtectionFailure,
    NoSpace,
    InvalidArgument,
    Failure,
    ResourceShortage,
    NotReceiver,
    NoAccess,
    MemoryFailure,
    MemoryError,
    AlreadyInSet,
    NotInSet,
    NameExists,
    Aborted,
    InvalidName,
    InvalidTask,
    InvalidRight,
    InvalidValue,
    UrefsOverflow,
    InvalidCapability,
    RightExists,
    InvalidHost,
    MemoryPresent,
    MemoryDataMoved,
    MemoryRestartCopy,
    InvalidProcessorSet,
    PolicyLimit,
    InvalidPolicy,
    InvalidObject,
    AlreadyWaiting,
    DefaultSet,
    ExceptionProtected,
    InvalidLedger,
    InvalidMemoryControl,
    InvalidSecurity,
    NotDepressed,
    Terminated,
    LockSetDestroyed,
    LockUnstable,
    LockOwned,
    LockOwnedSelf,
    SemaphoreDestroyed,
    RpcServerTerminated,
    RpcTerminateOrphan,
    RpcContinueOrphan,
    NotSupported,
    NodeDown,
    NotWaiting,
    OperationTimedOut,
    CodesignError,
    PolicyStatic,
    ReturnMax,
    Unknown(kern_return_t),
}

impl KernReturn {
    pub fn new(raw: kern_return_t) -> Self {
        use mach2::kern_return::*;

        // let raw = ((raw as u32) & ((1u32 << 28) - 1)) as i32;

        match raw {
            KERN_SUCCESS => Self::Success,
            KERN_INVALID_ADDRESS => Self::InvalidAddress,
            KERN_PROTECTION_FAILURE => Self::ProtectionFailure,
            KERN_NO_SPACE => Self::NoSpace,
            KERN_INVALID_ARGUMENT => Self::InvalidArgument,
            KERN_FAILURE => Self::Failure,
            KERN_RESOURCE_SHORTAGE => Self::ResourceShortage,
            KERN_NOT_RECEIVER => Self::NotReceiver,
            KERN_NO_ACCESS => Self::NoAccess,
            KERN_MEMORY_FAILURE => Self::MemoryFailure,
            KERN_MEMORY_ERROR => Self::MemoryError,
            KERN_ALREADY_IN_SET => Self::AlreadyInSet,
            KERN_NOT_IN_SET => Self::NotInSet,
            KERN_NAME_EXISTS => Self::NameExists,
            KERN_ABORTED => Self::Aborted,
            KERN_INVALID_NAME => Self::InvalidName,
            KERN_INVALID_TASK => Self::InvalidTask,
            KERN_INVALID_RIGHT => Self::InvalidRight,
            KERN_INVALID_VALUE => Self::InvalidValue,
            KERN_UREFS_OVERFLOW => Self::UrefsOverflow,
            KERN_INVALID_CAPABILITY => Self::InvalidCapability,
            KERN_RIGHT_EXISTS => Self::RightExists,
            KERN_INVALID_HOST => Self::InvalidHost,
            KERN_MEMORY_PRESENT => Self::MemoryPresent,
            KERN_MEMORY_DATA_MOVED => Self::MemoryDataMoved,
            KERN_MEMORY_RESTART_COPY => Self::MemoryRestartCopy,
            KERN_INVALID_PROCESSOR_SET => Self::InvalidProcessorSet,
            KERN_POLICY_LIMIT => Self::PolicyLimit,
            KERN_INVALID_POLICY => Self::InvalidPolicy,
            KERN_INVALID_OBJECT => Self::InvalidObject,
            KERN_ALREADY_WAITING => Self::AlreadyWaiting,
            KERN_DEFAULT_SET => Self::DefaultSet,
            KERN_EXCEPTION_PROTECTED => Self::ExceptionProtected,
            KERN_INVALID_LEDGER => Self::InvalidLedger,
            KERN_INVALID_MEMORY_CONTROL => Self::InvalidMemoryControl,
            KERN_INVALID_SECURITY => Self::InvalidSecurity,
            KERN_NOT_DEPRESSED => Self::NotDepressed,
            KERN_TERMINATED => Self::Terminated,
            KERN_LOCK_SET_DESTROYED => Self::LockSetDestroyed,
            KERN_LOCK_UNSTABLE => Self::LockUnstable,
            KERN_LOCK_OWNED => Self::LockOwned,
            KERN_LOCK_OWNED_SELF => Self::LockOwnedSelf,
            KERN_SEMAPHORE_DESTROYED => Self::SemaphoreDestroyed,
            KERN_RPC_SERVER_TERMINATED => Self::RpcServerTerminated,
            KERN_RPC_TERMINATE_ORPHAN => Self::RpcTerminateOrphan,
            KERN_RPC_CONTINUE_ORPHAN => Self::RpcContinueOrphan,
            KERN_NOT_SUPPORTED => Self::NotSupported,
            KERN_NODE_DOWN => Self::NodeDown,
            KERN_NOT_WAITING => Self::NotWaiting,
            KERN_OPERATION_TIMED_OUT => Self::OperationTimedOut,
            KERN_CODESIGN_ERROR => Self::CodesignError,
            KERN_POLICY_STATIC => Self::PolicyStatic,
            KERN_RETURN_MAX => Self::ReturnMax,
            v => Self::Unknown(v),
        }
    }

    pub fn is_ok(self) -> bool {
        self == KernReturn::Success
    }

    pub fn as_result(self) -> Result<(), Self> {
        match self.is_ok() {
            true => Ok(()),
            false => Err(self),
        }
    }

    #[track_caller]
    pub fn unwrap(self) {
        assert!(self.is_ok(), "{self:?}");
    }
}
