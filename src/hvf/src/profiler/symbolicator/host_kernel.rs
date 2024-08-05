use ahash::AHashMap;
use anyhow::anyhow;

use crate::profiler::transform::HostSyscallTransform;

use super::{SymbolResult, Symbolicator};

// fake symbolicator to inject host kernel ktrace events
pub struct HostKernelSymbolicator {
    trace_codes: AHashMap<u32, String>,
}

impl HostKernelSymbolicator {
    pub const IMAGE: &'static str = "xnu";

    pub const MSC_HV_TRAP: &'static str = "MSC_hv_trap";

    const ADDR_BASE: u64 = 0xffff000000000000;
    pub const ADDR_VMFAULT: u64 = Self::ADDR_BASE + 1;
    pub const ADDR_THREAD_SUSPENDED: u64 = Self::ADDR_BASE + 2;
    pub const ADDR_THREAD_WAIT: u64 = Self::ADDR_BASE + 3;
    pub const ADDR_THREAD_WAIT_UNINTERRUPTIBLE: u64 = Self::ADDR_BASE + 4;
    pub const ADDR_THREAD_HALTED: u64 = Self::ADDR_BASE + 5;

    // use ktrace codes to map BSD and Mach syscall numberes to names in a maintainable way
    const ADDR_TRACE_CODES: u64 = Self::ADDR_BASE + 0x1000;
    const ADDR_TRACE_BSD_SYSCALL: u64 = Self::ADDR_TRACE_CODES + 0x40c0000;
    const ADDR_TRACE_MACH_SYSCALL: u64 = Self::ADDR_TRACE_CODES + 0x10c0000;

    const ADDR_TRACE_HV_TRAP: u64 =
        Self::addr_for_syscall(HostSyscallTransform::SYSCALL_MACH_HV_TRAP_ARM64 as i64);

    pub const fn addr_for_syscall(num: i64) -> u64 {
        // Mach syscalls are negative, BSD syscalls are positive
        if num < 0 {
            Self::ADDR_TRACE_MACH_SYSCALL + (-num) as u64 * 4
        } else {
            Self::ADDR_TRACE_BSD_SYSCALL + num as u64 * 4
        }
    }

    pub fn new() -> anyhow::Result<Self> {
        let mut trace_codes = AHashMap::new();
        let file = std::fs::read_to_string("/usr/share/misc/trace.codes")?;
        for line in file.lines() {
            let mut split = line.split_ascii_whitespace();

            let code = split
                .next()
                .ok_or_else(|| anyhow!("invalid trace.codes line: {}", line))?;
            // to_lowercase because there's a line with "0X3134000"
            let code =
                u32::from_str_radix(code.to_lowercase().strip_prefix("0x").unwrap_or(code), 16)
                    .map_err(|_| anyhow!("invalid trace.codes line: {}", line))?;

            let name = split
                .next()
                .ok_or_else(|| anyhow!("invalid trace.codes line: {}", line))?;

            trace_codes.insert(code, name.to_string());
        }

        Ok(Self { trace_codes })
    }
}

impl Symbolicator for HostKernelSymbolicator {
    fn addr_to_symbol(&mut self, addr: u64) -> anyhow::Result<Option<SymbolResult>> {
        Ok(Some(SymbolResult {
            image: Self::IMAGE.to_string(),
            image_base: 0,
            symbol_offset: match addr {
                Self::ADDR_VMFAULT => Some(("vm_fault".to_string(), 0)),
                Self::ADDR_THREAD_SUSPENDED => Some(("thread_suspended".to_string(), 0)),
                Self::ADDR_THREAD_WAIT => Some(("thread_wait".to_string(), 0)),
                Self::ADDR_THREAD_WAIT_UNINTERRUPTIBLE => {
                    Some(("thread_wait_uninterruptible".to_string(), 0))
                }
                Self::ADDR_THREAD_HALTED => Some(("thread_halted".to_string(), 0)),

                // fix the "MSC_kern_invalid_5" eyesore. there's no trace code for this
                Self::ADDR_TRACE_HV_TRAP => Some((Self::MSC_HV_TRAP.to_string(), 0)),

                Self::ADDR_TRACE_CODES.. => {
                    let code = (addr - Self::ADDR_TRACE_CODES) as u32;
                    let name = self
                        .trace_codes
                        .get(&code)
                        .cloned()
                        .unwrap_or_else(|| format!("TRACE_unknown_{:x}", code));
                    Some((name, 0))
                }

                _ => None,
            },
        }))
    }
}
