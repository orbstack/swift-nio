# Private APIs & assumptions

Avoid it if possible... but sometimes private APIs are the best way to get things done.

Mach APIs are public, but not really documented and less common ones can occasionally break. Same goes for sysctls.

## vmgr

- NFS: raw NFS `mount` API, configuration passed as XDR
- sysctls: `kern.osversion`, `kern.osproductversion`
- `getsockopt(fd, IPPROTO_TCP, TCP_PEER_PID)`

## krun

- HVF: `hv_vcpu_t` is assumed to be the vCPU index (it's an opaque type)
  - **This needs to be fixed**
- HVF: `_hv_vcpu_get_context` to find vCPU block for ACTLR_EL1
- HVF: `ACTLR_EL1` is assumed to be at `offsetof(SCTLR_EL1) - 8` in the vCPU block
- HVF / M1–M3 CPU: `ACTLR_EL1_EnTSO` = bit 1; Rosetta also uses `ACTLR_EL1_MYSTERY` which is `0x200`
- IOKitHID: `kIOHIDRequiresTCCAuthorizationKey` = `RequiresTCCAuthorization` on device
- Mach (public): `mach_vm_map`, `mach_vm_remap`, `mach_vm_deallocate`, `semaphore_*`
- Rosetta: Linux executable is at `/Library/Apple/usr/libexec/oah/RosettaLinux/rosetta`
- Rosetta: ioctl `0x80456122` is for key, `0x45` bytes, static key, can be extracted from VZF
- Rosetta: ioctl `0x80806123` is for AOT config, 128 bytes, byte 0 = abstract or path for Unix socket, rest = path
- Rosetta: ioctl `0x6124` is for setting TSO at start, no side effects
- `os_unfair_lock_lock_with_options` on macOS 14 and below
  - This is OK because we use the public `os_unfair_lock_lock_with_flags` on macOS 15+

### Balloon

- HVF: `hv_vm_protect` can be used on partial ranges
- HVF: `hv_vm_protect` will carve out and combine ranges
- HVF: `hv_vm_protect` to NONE, then to RWX, clears pmap ledgers
- Remapping or protect(NONE)+protect(RWX) clears pmap ledgers
- pmap ledgers are lazily incremented on fault

### Profiler

- `ktrace` command
- HVF: Mach HV trap syscall is `-0x5` on arm64
- HVF: `hv_vcpu_run` is the direct caller of `hv_trap` which makes the syscall
  - **This is not true on macOS 15**
- XNU syscall instruction is `svc 0x80` on arm64
- sysctls: `kern.osproductversion`, `machdep.cpu.brand_string`
- Mach (public): `mach_wait_until`, `task_threads`, `mach_vm_deallocate`, `thread_info(THREAD_IDENTIFIER_INFO)`, `thread_policy_set`, `mach_port_deallocate`, `thread_get_state`, `thread_suspend`, `thread_resume`, `mach_vm_read`
- proc_pidinfo (semi-public): `proc_pid_rusage(RUSAGE_INFO_V0)`, `proc_pidinfo(PROC_PIDTHREADID64INFO)`
