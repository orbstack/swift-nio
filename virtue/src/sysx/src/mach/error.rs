use std::fmt::Display;

use mach2::kern_return::{kern_return_t, KERN_SUCCESS};

// missing in mach2
const MACH_SEND_INVALID_OPTIONS: kern_return_t = 0x10000013;
const MACH_SEND_AUX_TOO_SMALL: kern_return_t = 0x10000018;
const MACH_SEND_AUX_TOO_LARGE: kern_return_t = 0x10000019;
const MACH_RCV_INVALID_ARGUMENTS: kern_return_t = 0x10004013;

pub type MachResult<T> = Result<T, MachError>;

#[derive(Debug, Copy, Clone, Hash, Eq, PartialEq)]
#[must_use]
pub enum MachError {
    // kern_return
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

    // mach_msg
    MachMsgIpcSpace,
    MachMsgVmSpace,
    MachMsgIpcKernel,
    MachMsgVmKernel,

    SendInProgress,
    SendInvalidData,
    SendInvalidDest,
    SendTimedOut,
    SendInvalidVoucher,
    SendInterrupted,
    SendMsgTooSmall,
    SendInvalidReply,
    SendInvalidRight,
    SendInvalidNotify,
    SendInvalidMemory,
    SendNoBuffer,
    SendTooLarge,
    SendInvalidType,
    SendInvalidHeader,
    SendInvalidTrailer,
    SendInvalidContext,
    SendInvalidOptions,
    SendInvalidRtOolSize,
    SendNoGrantDest,
    SendMsgFiltered,
    SendAuxTooSmall,
    SendAuxTooLarge,

    RcvInProgress,
    RcvInvalidName,
    RcvTimedOut,
    RcvTooLarge,
    RcvInterrupted,
    RcvPortChanged,
    RcvInvalidNotify,
    RcvInvalidData,
    RcvPortDied,
    RcvInSet,
    RcvHeaderError,
    RcvBodyError,
    RcvInvalidType,
    RcvScatterSmall,
    RcvInvalidTrailer,
    RcvInProgressTimed,
    RcvInvalidReply,
    RcvInvalidArguments,

    Unknown,
}

// anyhow::Error compatibility
impl Display for MachError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{:?}", self)
    }
}

impl MachError {
    // better codegen for success
    #[cold]
    fn from_raw(raw: kern_return_t) -> Self {
        use mach2::kern_return::*;
        use mach2::message::*;

        match raw {
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

            MACH_MSG_IPC_SPACE => Self::MachMsgIpcSpace,
            MACH_MSG_VM_SPACE => Self::MachMsgVmSpace,
            MACH_MSG_IPC_KERNEL => Self::MachMsgIpcKernel,
            MACH_MSG_VM_KERNEL => Self::MachMsgVmKernel,

            MACH_SEND_IN_PROGRESS => Self::SendInProgress,
            MACH_SEND_INVALID_DATA => Self::SendInvalidData,
            MACH_SEND_INVALID_DEST => Self::SendInvalidDest,
            MACH_SEND_TIMED_OUT => Self::SendTimedOut,
            MACH_SEND_INVALID_VOUCHER => Self::SendInvalidVoucher,
            MACH_SEND_INTERRUPTED => Self::SendInterrupted,
            MACH_SEND_MSG_TOO_SMALL => Self::SendMsgTooSmall,
            MACH_SEND_INVALID_REPLY => Self::SendInvalidReply,
            MACH_SEND_INVALID_RIGHT => Self::SendInvalidRight,
            MACH_SEND_INVALID_NOTIFY => Self::SendInvalidNotify,
            MACH_SEND_INVALID_MEMORY => Self::SendInvalidMemory,
            MACH_SEND_NO_BUFFER => Self::SendNoBuffer,
            MACH_SEND_TOO_LARGE => Self::SendTooLarge,
            MACH_SEND_INVALID_TYPE => Self::SendInvalidType,
            MACH_SEND_INVALID_HEADER => Self::SendInvalidHeader,
            MACH_SEND_INVALID_TRAILER => Self::SendInvalidTrailer,
            MACH_SEND_INVALID_CONTEXT => Self::SendInvalidContext,
            MACH_SEND_INVALID_OPTIONS => Self::SendInvalidOptions,
            MACH_SEND_INVALID_RT_OOL_SIZE => Self::SendInvalidRtOolSize,
            MACH_SEND_NO_GRANT_DEST => Self::SendNoGrantDest,
            MACH_SEND_MSG_FILTERED => Self::SendMsgFiltered,
            MACH_SEND_AUX_TOO_SMALL => Self::SendAuxTooSmall,
            MACH_SEND_AUX_TOO_LARGE => Self::SendAuxTooLarge,

            MACH_RCV_IN_PROGRESS => Self::RcvInProgress,
            MACH_RCV_INVALID_NAME => Self::RcvInvalidName,
            MACH_RCV_TIMED_OUT => Self::RcvTimedOut,
            MACH_RCV_TOO_LARGE => Self::RcvTooLarge,
            MACH_RCV_INTERRUPTED => Self::RcvInterrupted,
            MACH_RCV_PORT_CHANGED => Self::RcvPortChanged,
            MACH_RCV_INVALID_NOTIFY => Self::RcvInvalidNotify,
            MACH_RCV_INVALID_DATA => Self::RcvInvalidData,
            MACH_RCV_PORT_DIED => Self::RcvPortDied,
            MACH_RCV_IN_SET => Self::RcvInSet,
            MACH_RCV_HEADER_ERROR => Self::RcvHeaderError,
            MACH_RCV_BODY_ERROR => Self::RcvBodyError,
            MACH_RCV_INVALID_TYPE => Self::RcvInvalidType,
            MACH_RCV_SCATTER_SMALL => Self::RcvScatterSmall,
            MACH_RCV_INVALID_TRAILER => Self::RcvInvalidTrailer,
            MACH_RCV_IN_PROGRESS_TIMED => Self::RcvInProgressTimed,
            MACH_RCV_INVALID_REPLY => Self::RcvInvalidReply,
            MACH_RCV_INVALID_ARGUMENTS => Self::RcvInvalidArguments,

            _ => Self::Unknown,
        }
    }

    pub fn result(raw: kern_return_t) -> MachResult<()> {
        match raw {
            KERN_SUCCESS => Ok(()),
            _ => Err(Self::from_raw(raw)),
        }
    }
}
