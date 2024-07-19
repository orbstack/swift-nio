// Copyright 2018 Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0
//
// Portions Copyright 2017 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the THIRD-PARTY file.

use gruel::ParkSignalChannelExt;
use gruel::SignalChannel;
use gruel::StartupAbortedError;
use gruel::StartupSignal;
use gruel::StartupTask;
use gruel::Waker;
use hvf::on_vm_park;
use hvf::on_vm_unpark;
use hvf::HvVcpuRef;
use std::io;
use std::result;
use std::sync::Arc;
use std::thread::{self, Thread};
use std::time::Duration;
use utils::Mutex;
use vmm_ids::ArcVcpuSignal;
use vmm_ids::VcpuSignal;
use vmm_ids::VcpuSignalMask;
use vmm_ids::VmmShutdownSignal;

use super::super::TimestampUs;
use crate::vmm_config::machine_config::CpuFeaturesTemplate;
use crate::VmmShutdownHandle;

use arch;
#[cfg(target_arch = "aarch64")]
use arch::aarch64::gic::GICDevice;
use crossbeam_channel::{unbounded, Receiver, Sender};
use devices::legacy::{Gic, GicVcpuHandle, WfeThread};
use hvf::{HvfVcpu, HvfVm, Parkable, VcpuExit};
use utils::eventfd::EventFd;
use vm_memory::{
    Address, GuestAddress, GuestMemory, GuestMemoryError, GuestMemoryMmap, GuestMemoryRegion,
};

/// Errors associated with the wrappers over KVM ioctls.
#[derive(thiserror::Error, Debug)]
pub enum Error {
    #[error("failed to configure guest memory: {0}")]
    GuestMemoryMmap(GuestMemoryError),
    #[error("not enough memory slots")]
    NotEnoughMemorySlots,
    #[cfg(target_arch = "aarch64")]
    #[error("error configuring the general purpose aarch64 registers: {0:?}")]
    REGSConfiguration(arch::aarch64::regs::Error),
    #[cfg(target_arch = "aarch64")]
    #[error("error setting up the global interrupt controller: {0:?}")]
    SetupGIC(arch::aarch64::gic::Error),
    #[error("cannot set the memory regions: {0}")]
    SetUserMemoryRegion(hvf::Error),
    #[error("failed to signal Vcpu: {0}")]
    SignalVcpu(utils::errno::Error),
    #[error("error doing Vcpu Init on Arm")]
    VcpuArmInit,
    #[error("error getting the Vcpu preferred target on Arm")]
    VcpuArmPreferredTarget,
    #[error("vCPU count is not initialized")]
    VcpuCountNotInitialized,
    #[error("cannot run the VCPUs")]
    VcpuRun,
    #[error("cannot spawn a new vCPU thread: {0}")]
    VcpuSpawn(io::Error),
    #[error("vCPU thread failed to initialize")]
    VcpuInit,
    #[error("vcpu not present in TLS")]
    VcpuTlsNotPresent,
    #[error("unexpected KVM_RUN exit reason")]
    VcpuUnhandledKvmExit,
    #[error("cannot configure the VM: {0}")]
    VmSetup(hvf::Error),
}

pub type Result<T> = result::Result<T, Error>;

/// A wrapper around creating and using a VM.
pub struct Vm {
    shutdown: VmmShutdownSignal,
    pub hvf_vm: HvfVm,
    parker: Arc<VmParker>,
    #[cfg(target_arch = "aarch64")]
    irqchip_handle: Option<Box<dyn GICDevice>>,
}

pub struct VmParker {
    hvf_vm: HvfVm,
    vcpus: Mutex<Vec<ArcVcpuSignal>>,

    /// Tasks here represent vCPUs which have yet to park.
    park_signal: StartupSignal,

    /// Tasks here represent parker threads which have yet to finish their operations e.g. balloon.
    unpark_signal: StartupSignal,

    /// These are the regions mapped to the VM.
    regions: Mutex<Vec<MapRegion>>,
}

pub struct MapRegion {
    host_start_addr: u64,
    guest_start_addr: u64,
    size: u64,
}

impl VmParker {
    pub fn new(hvf_vm: HvfVm) -> Self {
        Self {
            hvf_vm,
            vcpus: Default::default(),
            park_signal: Default::default(),
            unpark_signal: Default::default(),
            regions: Default::default(),
        }
    }

    pub fn set_regions(&self, new_regions: Vec<MapRegion>) {
        let mut regions = self.regions.lock().unwrap();
        *regions = new_regions;
    }
}

impl Parkable for VmParker {
    fn register_vcpu(&self, vcpu: ArcVcpuSignal) -> StartupTask {
        self.vcpus.lock().unwrap().push(vcpu);

        // Won't panic: `park_signal` is only ever used in a panic-less context
        self.park_signal.resurrect_cloned().unwrap()
    }

    fn park(&self) -> std::result::Result<StartupTask, StartupAbortedError> {
        // Resurrect the unpark task. We do this here to ensure that parking vCPUs don't
        // immediately exit.
        let unpark_task = self
            .unpark_signal
            .resurrect_cloned()
            .expect("`unpark_signal` poisoned");

        // Let's send a pause signal to every vCPU. They will receive and honor this since this
        // signal is never asserted outside of `park` (or when the signal is aborted)
        for cpu in &*self.vcpus.lock().unwrap() {
            cpu.assert(VcpuSignalMask::PAUSE);
        }

        // Now, wait for every vCPU to enter the parked state. If a shutdown occurs, this signal will
        // be aborted and we'll unblock naturally.
        self.park_signal.wait()?;

        // Now, we can unmap the vCPU memory.
        // let regions = self.regions.lock().unwrap();
        // for region in regions.iter() {
        //     debug!(
        //         "unmap_memory: {:x} {:x}",
        //         region.guest_start_addr, region.size
        //     );
        //     self.hvf_vm
        //         .unmap_memory(region.guest_start_addr, region.size)
        //         .unwrap();
        // }
        on_vm_park().unwrap();

        // From there, we just have to give the consumer the unpark task so they can eventually
        // resolve it.
        Ok(unpark_task)
    }

    fn unpark(&self, unpark_task: StartupTask) {
        on_vm_unpark().unwrap();

        // Make sure we remap all regions before resolving the startup task.
        // let regions = self.regions.lock().unwrap();
        // for region in regions.iter() {
        //     debug!(
        //         "map_memory: {:x} {:x} {:x}",
        //         region.host_start_addr, region.guest_start_addr, region.size
        //     );
        //     self.hvf_vm
        //         .map_memory(region.host_start_addr, region.guest_start_addr, region.size)
        //         .unwrap();
        // }

        unpark_task.success();
    }

    fn process_park_commands(
        &self,
        taken: VcpuSignalMask,
        park_task: StartupTask,
    ) -> std::result::Result<StartupTask, StartupAbortedError> {
        // Check whether the signal needs to be resolved.
        if !taken.contains(VcpuSignalMask::PAUSE) {
            return Ok(park_task);
        }

        // Tell the parker that we successfully parked.
        let park_task = park_task.success_keeping();

        // Now, we really need to wait on the unpark signal.
        self.unpark_signal.wait()?;

        // And we're back in business!
        // Won't panic: `park_signal` is only ever used in a panic-less context
        Ok(park_task.resurrect().unwrap())
    }

    fn dump_debug(&self) {
        for cpu in &*self.vcpus.lock().unwrap() {
            cpu.assert(VcpuSignalMask::DUMP_DEBUG);
        }
    }
}

impl Vm {
    /// Constructs a new `Vm` using the given `Kvm` instance.
    pub fn new(
        shutdown: VmmShutdownSignal,
        vcpu_count: u8,
        guest_mem: &GuestMemoryMmap,
    ) -> Result<Self> {
        let hvf_vm = HvfVm::new(guest_mem, vcpu_count).map_err(Error::VmSetup)?;

        Ok(Vm {
            shutdown,
            hvf_vm: hvf_vm.clone(),
            parker: Arc::new(VmParker::new(hvf_vm.clone())),
            #[cfg(target_arch = "aarch64")]
            irqchip_handle: None,
        })
    }

    /// Initializes the guest memory.
    pub fn memory_init(&mut self, guest_mem: &GuestMemoryMmap) -> Result<()> {
        let mut map_regions = Vec::new();
        for region in guest_mem.iter() {
            // It's safe to unwrap because the guest address is valid.
            let host_addr = guest_mem.get_host_address(region.start_addr()).unwrap();
            debug!(
                "Guest memory host_addr={:x?} guest_addr={:x?} len={:x?}",
                host_addr,
                region.start_addr().raw_value(),
                region.len()
            );
            self.hvf_vm
                .map_memory(
                    host_addr as u64,
                    region.start_addr().raw_value(),
                    region.len(),
                )
                .map_err(Error::SetUserMemoryRegion)?;
            map_regions.push(MapRegion {
                host_start_addr: host_addr as u64,
                guest_start_addr: region.start_addr().raw_value(),
                size: region.len(),
            });
        }

        self.parker.set_regions(map_regions);
        Ok(())
    }

    #[cfg(target_arch = "aarch64")]
    pub fn setup_irqchip(&mut self, vcpu_count: u8) -> Result<()> {
        self.irqchip_handle = if let Some(gic) = self.hvf_vm.get_fdt_gic() {
            Some(gic)
        } else {
            Some(
                arch::aarch64::gic::create_userspace_gic(vcpu_count.into())
                    .map_err(Error::SetupGIC)?,
            )
        };
        Ok(())
    }

    #[cfg(target_arch = "x86_64")]
    pub fn setup_irqchip(&mut self) -> Result<()> {
        Ok(())
    }

    /// Gets a reference to the irqchip of the VM
    #[cfg(target_arch = "aarch64")]
    #[allow(clippy::borrowed_box)]
    pub fn get_irqchip(&self) -> &Box<dyn GICDevice> {
        self.irqchip_handle.as_ref().unwrap()
    }

    pub fn add_mapping(
        &self,
        reply_sender: Sender<bool>,
        host_addr: u64,
        guest_addr: u64,
        len: u64,
    ) {
        debug!("add_mapping: host_addr={host_addr:x}, guest_addr={guest_addr:x}, len={len}");
        if let Err(e) = self.hvf_vm.unmap_memory(guest_addr, len) {
            error!("Error removing memory map: {:?}", e);
        }

        if let Err(e) = self.hvf_vm.map_memory(host_addr, guest_addr, len) {
            error!("Error adding memory map: {:?}", e);
            reply_sender.send(false).unwrap();
        } else {
            reply_sender.send(true).unwrap();
        }
    }

    pub fn remove_mapping(&self, reply_sender: Sender<bool>, guest_addr: u64, len: u64) {
        debug!("remove_mapping: guest_addr={guest_addr:x}, len={len}");
        if let Err(e) = self.hvf_vm.unmap_memory(guest_addr, len) {
            error!("Error removing memory map: {:?}", e);
            reply_sender.send(false).unwrap();
        } else {
            reply_sender.send(true).unwrap();
        }
    }

    pub fn get_parker(&self) -> Arc<VmParker> {
        self.parker.clone()
    }

    pub fn destroy_hvf(&self) {
        self.hvf_vm.destroy();
    }
}

/// Encapsulates configuration parameters for the guest vCPUS.
#[derive(Debug, Eq, PartialEq)]
pub struct VcpuConfig {
    /// Number of guest VCPUs.
    pub vcpu_count: u8,
    /// Enable hyperthreading in the CPUID configuration.
    pub ht_enabled: bool,
    /// CPUID template to use.
    pub cpu_template: Option<CpuFeaturesTemplate>,
    #[cfg(target_arch = "aarch64")]
    pub enable_tso: bool,
}

/// A wrapper around creating and using a kvm-based VCPU.
pub struct Vcpu {
    id: u8,
    boot_receiver: Receiver<GuestAddress>,
    boot_senders: Option<Vec<Sender<GuestAddress>>>,
    #[cfg(target_arch = "aarch64")]
    fdt_addr: u64,
    guest_mem: GuestMemoryMmap,
    #[cfg(target_arch = "aarch64")]
    enable_tso: bool,
    mmio_bus: Option<devices::Bus>,
    #[cfg_attr(all(test, target_arch = "aarch64"), allow(unused))]
    exit_evt: EventFd,

    shutdown: VmmShutdownSignal,

    #[cfg(target_arch = "aarch64")]
    mpidr: u64,

    #[cfg(target_arch = "aarch64")]
    intc: Arc<Mutex<Gic>>,

    #[cfg(target_arch = "aarch64")]
    csmap_path: Option<Arc<String>>,

    #[cfg(target_arch = "x86_64")]
    hvf_vm: HvfVm,

    #[cfg(target_arch = "x86_64")]
    vcpu_count: u8,
    #[cfg(target_arch = "x86_64")]
    ht_enabled: bool,
}

impl Vcpu {
    // macOS doesn't use signals for kicking
    pub fn register_kick_signal_handler() {}

    /// Constructs a new VCPU for `vm`.
    ///
    /// # Arguments
    ///
    /// * `id` - Represents the CPU number between [0, max vcpus).
    /// * `vm_fd` - The kvm `VmFd` for the virtual machine this vcpu will get attached to.
    /// * `exit_evt` - An `EventFd` that will be written into when this vcpu exits.
    /// * `create_ts` - A timestamp used by the vcpu to calculate its lifetime.
    #[cfg(target_arch = "aarch64")]
    pub fn new_aarch64(
        id: u8,
        boot_receiver: Receiver<GuestAddress>,
        exit_evt: EventFd,
        guest_mem: GuestMemoryMmap,
        _create_ts: TimestampUs,
        intc: Arc<Mutex<Gic>>,
        shutdown: VmmShutdownSignal,
        csmap_path: Option<Arc<String>>,
    ) -> Result<Self> {
        Ok(Vcpu {
            id,
            boot_receiver,
            boot_senders: None,
            fdt_addr: 0,
            enable_tso: false,
            mmio_bus: None,
            exit_evt,
            guest_mem,
            mpidr: 0,
            intc,
            shutdown,
            csmap_path,
        })
    }

    #[cfg(target_arch = "x86_64")]
    pub fn new_x86_64(
        id: u8,
        boot_receiver: Receiver<GuestAddress>,
        exit_evt: EventFd,
        guest_mem: GuestMemoryMmap,
        vm: &Vm,
        shutdown: VmmShutdownSignal,
    ) -> Result<Self> {
        Ok(Vcpu {
            shutdown,
            id,
            boot_receiver,
            boot_senders: None,
            mmio_bus: None,
            exit_evt,
            guest_mem,
            hvf_vm: vm.hvf_vm.clone(),
            vcpu_count: 0,
            ht_enabled: false,
        })
    }

    /// Returns the cpu index as seen by the guest OS.
    pub fn cpu_index(&self) -> u8 {
        self.id
    }

    /// Gets the MPIDR register value.
    #[cfg(target_arch = "aarch64")]
    pub fn get_mpidr(&self) -> u64 {
        self.mpidr
    }

    /// Sets a MMIO bus for this vcpu.
    pub fn set_mmio_bus(&mut self, mmio_bus: devices::Bus) {
        self.mmio_bus = Some(mmio_bus);
    }

    pub fn set_boot_senders(&mut self, boot_senders: Vec<Sender<GuestAddress>>) {
        self.boot_senders = Some(boot_senders);
    }

    /// Configures an aarch64 specific vcpu.
    ///
    /// # Arguments
    ///
    /// * `vm_fd` - The kvm `VmFd` for this microvm.
    /// * `guest_mem` - The guest memory used by this microvm.
    /// * `kernel_load_addr` - Offset from `guest_mem` at which the kernel is loaded.
    #[cfg(target_arch = "aarch64")]
    pub fn configure_aarch64(
        &mut self,
        guest_mem: &GuestMemoryMmap,
        enable_tso: bool,
    ) -> Result<()> {
        self.mpidr = hvf::vcpu_id_to_mpidr(self.id as u64);
        self.fdt_addr = arch::aarch64::get_fdt_addr(guest_mem);
        self.enable_tso = enable_tso;

        Ok(())
    }

    #[cfg(target_arch = "x86_64")]
    pub fn configure_x86_64(
        &mut self,
        _guest_mem: &GuestMemoryMmap,
        vcpu_config: &VcpuConfig,
    ) -> Result<()> {
        self.vcpu_count = vcpu_config.vcpu_count;
        self.ht_enabled = vcpu_config.ht_enabled;
        Ok(())
    }

    /// Moves the vcpu to its own thread and constructs a VcpuHandle.
    /// The handle can be used to control the remote vcpu.
    pub fn start_threaded(mut self, parker: Arc<VmParker>) -> Result<VcpuHandle> {
        let (init_sender, init_receiver) = unbounded();
        let boot_sender = self.boot_senders.as_ref().unwrap()[self.cpu_index() as usize].clone();

        let vcpu_thread = thread::Builder::new()
            .name(format!("vcpu{}", self.cpu_index()))
            .spawn(move || {
                self.run(parker, init_sender);
            })
            .map_err(Error::VcpuSpawn)?;

        init_receiver.recv().map_err(|_| Error::VcpuInit)?;

        Ok(VcpuHandle::new(boot_sender, vcpu_thread))
    }

    /// Returns error or enum specifying whether emulation was handled or interrupted.
    #[cfg(target_arch = "aarch64")]
    fn run_emulation(
        &mut self,
        hvf_vcpu: &mut HvfVcpu,
        intc_handle: &mut dyn GicVcpuHandle,
    ) -> Result<VcpuEmulation> {
        use std::sync::atomic::Ordering;

        use devices::legacy::GicSysReg;
        use hvf::{wait_for_balloon, ExitActions};

        let vcpuid = hvf_vcpu.id();
        let pending_irq = intc_handle.get_pending_irq(&self.intc).map(|i| i.0);

        let (exit, exit_actions) = hvf_vcpu.run(pending_irq).expect("Failed to run HVF vCPU");

        // handle PV GIC read side effects
        let mmio_bus = self.mmio_bus.as_ref().unwrap();
        if exit_actions.contains(ExitActions::READ_IAR1_EL1) {
            mmio_bus.read_sysreg(vcpuid, GicSysReg::ICC_IAR1_EL1 as u64);
        }

        match exit {
            VcpuExit::Breakpoint => {
                debug!("vCPU {} breakpoint", vcpuid);
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::Canceled => {
                debug!("vCPU {} canceled", vcpuid);
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::CpuOn(mpidr, entry, context_id) => {
                debug!(
                    "CpuOn: mpidr=0x{:x} entry=0x{:x} context_id={}",
                    mpidr, entry, context_id
                );
                let cpuid: usize = (mpidr >> 8) as usize;
                let boot_senders = self.boot_senders.as_ref().unwrap();
                if let Some(sender) = boot_senders.get(cpuid) {
                    sender.send(GuestAddress(entry)).unwrap()
                }
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::HypervisorCall => {
                debug!("vCPU {} HVC", vcpuid);
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::HypervisorIoCall { dev_id, args_addr } => {
                debug!(
                    "vCPU {} HVC IO: dev_id={} args_addr={:?}",
                    vcpuid, dev_id, args_addr
                );
                let ret = mmio_bus.call_hvc(dev_id, args_addr);
                hvf_vcpu.write_gp_reg(0, ret as u64).unwrap();
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::MmioRead(addr, data) => {
                if !mmio_bus.read(vcpuid, addr, data) {
                    // unhandled MMIO read:
                    // either invalid address, or system RAM faulted (due to balloon)
                    if self.guest_mem.address_in_range(GuestAddress(addr)) {
                        // faulted during balloon, and falls within system RAM. retry insn
                        wait_for_balloon();
                        hvf_vcpu.clear_pending_mmio();
                    } else {
                        panic!("unhandled MMIO read at address 0x{:x}", addr);
                    }
                }
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::MmioWrite(addr, data) => {
                if !mmio_bus.write(vcpuid, addr, data) {
                    // unhandled MMIO write:
                    // either invalid address, or system RAM faulted (due to balloon)
                    if self.guest_mem.address_in_range(GuestAddress(addr)) {
                        // faulted during balloon, and falls within system RAM. retry insn
                        wait_for_balloon();
                        hvf_vcpu.clear_pending_mmio();
                    } else {
                        panic!("unhandled MMIO write at address 0x{:x}", addr);
                    }
                }
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::SecureMonitorCall => {
                debug!("vCPU {} SMC", vcpuid);
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::Shutdown => {
                debug!("vCPU {} received shutdown signal", vcpuid);
                Ok(VcpuEmulation::Stopped)
            }
            VcpuExit::SystemRegister {
                sys_reg,
                arg_reg_idx,
                is_read,
            } => {
                if is_read {
                    hvf_vcpu
                        .write_gp_reg(arg_reg_idx, mmio_bus.read_sysreg(vcpuid, sys_reg))
                        .unwrap()
                } else {
                    mmio_bus.write_sysreg(
                        vcpuid,
                        sys_reg,
                        hvf_vcpu.read_gp_reg(arg_reg_idx).unwrap(),
                    );
                }

                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::VtimerActivated => {
                debug!("vCPU {} VtimerActivated", vcpuid);
                intc_handle.set_vtimer_irq();
                Ok(VcpuEmulation::Handled)
            }
            VcpuExit::WaitForEvent => {
                debug!("vCPU {} WaitForEvent", vcpuid);
                Ok(VcpuEmulation::WaitForEvent)
            }
            VcpuExit::WaitForEventExpired => {
                debug!("vCPU {} WaitForEventExpired", vcpuid);
                Ok(VcpuEmulation::WaitForEventExpired)
            }
            VcpuExit::WaitForEventTimeout(duration) => {
                debug!("vCPU {} WaitForEventTimeout timeout={:?}", vcpuid, duration);
                Ok(VcpuEmulation::WaitForEventTimeout(duration))
            }
            VcpuExit::PvlockPark => Ok(VcpuEmulation::PvlockPark),
            VcpuExit::PvlockUnpark(vcpu) => Ok(VcpuEmulation::PvlockUnpark(vcpu)),
        }
    }

    /// Returns error or enum specifying whether emulation was handled or interrupted.
    #[cfg(target_arch = "x86_64")]
    fn run_emulation(&mut self, hvf_vcpu: &mut HvfVcpu) -> Result<VcpuEmulation> {
        use hvf::HV_X86_RAX;
        use nix::unistd::write;

        let vcpuid = hvf_vcpu.id();

        match hvf_vcpu.run() {
            Ok(exit) => match exit {
                VcpuExit::Canceled => {
                    debug!("vCPU {} canceled", vcpuid);
                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::CpuOn { cpus, entry_rip } => {
                    for (cpuid, &should_start) in cpus.iter().enumerate() {
                        if should_start {
                            debug!("start vCPU {} at {:x}", cpuid, entry_rip);
                            if let Some(boot_senders) = &self.boot_senders {
                                if let Some(sender) = boot_senders.get(cpuid) {
                                    sender.send(GuestAddress(entry_rip)).unwrap()
                                }
                            }
                        }
                    }

                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::Handled => Ok(VcpuEmulation::Handled),
                VcpuExit::HypervisorCall => {
                    debug!("vCPU {} HVC", vcpuid);
                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::HypervisorIoCall { dev_id, args_addr } => {
                    debug!(
                        "vCPU {} HVC IO: dev_id={} args_addr={}",
                        vcpuid, dev_id, args_addr
                    );
                    if let Some(ref mmio_bus) = self.mmio_bus {
                        let ret = mmio_bus.call_hvc(dev_id, args_addr);
                        hvf_vcpu.write_reg(HV_X86_RAX, ret as u64).unwrap();
                    }
                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::MmioRead(addr, data) => {
                    if let Some(ref mmio_bus) = self.mmio_bus {
                        if !mmio_bus.read(vcpuid as u64, addr, data) {
                            panic!("unhandled MMIO read at address 0x{:x}", addr);
                        }
                    }
                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::MmioWrite(addr, data) => {
                    if let Some(ref mmio_bus) = self.mmio_bus {
                        if !mmio_bus.write(vcpuid as u64, addr, data) {
                            panic!("unhandled MMIO write at address 0x{:x}", addr);
                        }
                    }
                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::Shutdown => {
                    debug!("vCPU {} received shutdown signal", vcpuid);
                    Ok(VcpuEmulation::Stopped)
                }
                VcpuExit::IoPortRead(_) => {
                    //debug!("vCPU {} IoPortRead port={}", vcpuid, port);
                    Ok(VcpuEmulation::Handled)
                }
                VcpuExit::IoPortWrite(port, value) => {
                    //debug!("vCPU {} IoPortWrite port={} value={}", vcpuid, port, value);
                    // write to stdout
                    if port == 0x3f8 {
                        write(1, &[(value & 0xff) as u8]).unwrap();
                    }
                    Ok(VcpuEmulation::Handled)
                }
            },
            Err(e) => {
                panic!("Error running HVF vCPU: {:?}", e);
            }
        }
    }

    /// Main loop of the vCPU thread.
    #[cfg(target_arch = "aarch64")]
    pub fn run(&mut self, parker: Arc<VmParker>, init_sender: Sender<bool>) {
        use gruel::{
            define_waker_set, BoundSignalChannel, DynamicallyBoundWaker, MultiShutdownSignalExt,
            ParkWaker, QueueRecvSignalChannelExt, ShutdownAlreadyRequestedExt,
        };
        use vmm_ids::VmmShutdownPhase;

        define_waker_set! {
            struct VcpuWakerSet {
                park: ParkWaker,
                dynamic: DynamicallyBoundWaker,
                hvf: HvfWaker,
            }
        }

        struct HvfWaker(HvVcpuRef);

        impl Waker for HvfWaker {
            fn wake(&self) {
                hvf::vcpu_request_exit(self.0).unwrap();
            }
        }

        // separate function so that this shows up in debug spindumps
        // this is debug *info*, not debug_assertions, so it includes release-with-debug
        #[cfg_attr(debug, inline(never))]
        fn wait_for_pvlock(signal: &Arc<SignalChannel<VcpuSignalMask, VcpuWakerSet>>) {
            // allow spurious wakeups from IRQs, as it's just a hint to try locking again.
            // pending IRQs will be sent when DAIF is restored after a spurious wakeup.
            signal.wait_on_park(VcpuSignalMask::ALL_WAIT | VcpuSignalMask::PVLOCK);

            // if there's a pending PV lock token (either new or existing), consume it
            _ = signal.take(VcpuSignalMask::PVLOCK);
        }

        // Create the underlying HVF vCPU.
        let mut hvf_vcpu =
            HvfVcpu::new(parker.clone(), self.guest_mem.clone()).expect("Can't create HVF vCPU");

        let hvf_vcpuid = hvf_vcpu.id();
        let hvf_vcpu_ref = hvf_vcpu.vcpu_ref();

        // Create and register the signal
        let signal = Arc::new(SignalChannel::new(VcpuWakerSet {
            park: ParkWaker::default(),
            dynamic: DynamicallyBoundWaker::default(),
            hvf: HvfWaker(hvf_vcpu_ref),
        }));
        let cpu_shutdown_task = self
            .shutdown
            .spawn_signal(
                VmmShutdownPhase::VcpuExitLoop,
                BoundSignalChannel::new(signal.clone(), VcpuSignalMask::EXIT_LOOP),
            )
            .unwrap_or_run_now();

        let hvf_destroy_task = self
            .shutdown
            .spawn_signal(
                VmmShutdownPhase::VcpuDestroy,
                BoundSignalChannel::new(signal.clone(), VcpuSignalMask::DESTROY_VM),
            )
            .unwrap_or_run_now();

        // Register the vCPU with the interrupt controller and the parker
        self.intc.lock().unwrap().register_vcpu(
            hvf_vcpuid,
            WfeThread {
                hv_vcpu: hvf_vcpu.vcpu_ref(),
                signal: signal.clone(),
            },
        );

        let mut park_task = parker.register_vcpu(signal.clone());

        devices::virtio::fs::macos::iopolicy::prepare_vcpu_for_hvc().unwrap();

        // Notify init done (everything is registered)
        // TODO: We should be using startup signals for these to reduce the number of thread wake-ups.
        init_sender.send(true).unwrap();

        // Wait for boot signal
        let Ok(entry_addr) =
            signal.recv_with_cancel(VcpuSignalMask::ANY_SHUTDOWN, &self.boot_receiver)
        else {
            // Destroy both aforementioned tasks—the user has requested a shutdown.
            return;
        };

        let entry_addr = entry_addr.raw_value();

        // Create a guard for loop exit
        let shutdown_handle = VmmShutdownHandle(self.exit_evt.try_clone().unwrap());
        let vcpu_loop_exit_guard = scopeguard::guard((), |()| {
            shutdown_handle.request_shutdown();
        });

        let vcpu_loop_exit_guard = (vcpu_loop_exit_guard, cpu_shutdown_task);

        // Setup the vCPU
        hvf_vcpu
            .set_initial_state(entry_addr, self.fdt_addr, self.mpidr, self.enable_tso)
            .unwrap_or_else(|e| panic!("Can't set HVF vCPU {} initial state: {}", hvf_vcpuid, e));

        // Finally, start virtualization!
        let mut intc_vcpu_handle = self.intc.lock().unwrap().get_vcpu_handle(hvf_vcpuid);

        loop {
            // Handle events
            let taken = signal.take(VcpuSignalMask::ALL_WAIT);
            if taken.contains(VcpuSignalMask::EXIT_LOOP) {
                break;
            }

            // (this should never happen if we haven't exited the loop yet)
            debug_assert!(!taken.contains(VcpuSignalMask::DESTROY_VM));

            let Ok(park_task_tmp) = parker.process_park_commands(taken, park_task) else {
                error!(
                    "Thread responsible for unparking vCPUs aborted the operation; shutting down!"
                );
                break;
            };
            park_task = park_task_tmp;

            if taken.contains(VcpuSignalMask::INTERRUPT) {
                // Although we could theoretically use this to signal the presence of an interrupt,
                // we already use the lockless GIC handle to determine which interrupt was sent to
                // us so there's no real performance reason for doing this.
            }

            #[cfg(target_arch = "aarch64")]
            if taken.contains(VcpuSignalMask::DUMP_DEBUG) {
                // dump state
                match hvf_vcpu.dump_debug(self.csmap_path.as_ref().map(|p| p.as_str())) {
                    Ok(dump) => info!("vCPU {} state:\n{}", hvf_vcpuid, dump),
                    Err(e) => error!("Failed to dump vCPU state: {}", e),
                }
            }

            // Run emulation
            let emulation = signal.wait(VcpuSignalMask::ALL_WAIT, VcpuWakerSet::hvf, || {
                self.run_emulation(&mut hvf_vcpu, &mut *intc_vcpu_handle)
            });

            // Handle emulation result
            let Some(emulation) = emulation else {
                // This is a VM exit, which has no side-effects.
                continue;
            };

            match emulation {
                // Emulation ran successfully, continue.
                Ok(VcpuEmulation::Handled) => {}

                // Wait for an external event.
                // N.B. we check `vcpu_should_wait` here since, although we consistently assert the
                // wake-up signal on new interrupts, we don't re-assert it for self-PPI and EOI so
                // making sure that the guest doesn't WFE while an IRQ is pending seems like a smart
                // idea.
                Ok(VcpuEmulation::WaitForEvent) => {
                    if intc_vcpu_handle.should_wait(&self.intc) {
                        signal.wait_on_park(VcpuSignalMask::ALL_WAIT);
                    }
                }
                Ok(VcpuEmulation::WaitForEventExpired) => {}
                Ok(VcpuEmulation::WaitForEventTimeout(timeout)) => {
                    if intc_vcpu_handle.should_wait(&self.intc) {
                        signal.wait_on_park_timeout(VcpuSignalMask::ALL_WAIT, timeout);
                    }
                }

                // The guest was rebooted or halted. No need to check for the shutdown signal—we
                // can't re-renter the VM anyways.Emulation errors lead to vCPU exit.
                Ok(VcpuEmulation::Stopped) | Err(_) => {
                    break;
                }

                // PV-lock
                Ok(VcpuEmulation::PvlockPark) => {
                    wait_for_pvlock(&signal);
                }
                Ok(VcpuEmulation::PvlockUnpark(vcpuid)) => {
                    self.intc.lock().unwrap().kick_vcpu_for_pvlock(vcpuid);
                }
            }
        }

        drop(vcpu_loop_exit_guard);

        // Now, we just have to wait for the permission to destroy our vCPUs. We can't just let these
        // become dangling handles because cause an entire list of vCPUs to exit requires all handles
        // in that list to exit successfully.
        //
        // We really don't want any other signal to shutdown the CPU. Note: `wait_on_park` only wakes
        // up if we genuinely receive the signal.
        signal.wait_on_park(VcpuSignalMask::DESTROY_VM);

        hvf_vcpu.destroy();
        drop(hvf_destroy_task);
    }

    /// Main loop of the vCPU thread.
    #[cfg(target_arch = "x86_64")]
    pub fn run(&mut self, parker: Arc<VmParker>, init_sender: Sender<bool>) {
        use gruel::{
            define_waker_set, BoundSignalChannel, DynamicallyBoundWaker, MultiShutdownSignalExt,
            ParkSignalChannelExt, ParkWaker, QueueRecvSignalChannelExt,
            ShutdownAlreadyRequestedExt, SignalChannel,
        };
        use vmm_ids::VmmShutdownPhase;

        define_waker_set! {
            struct VcpuWakerSet {
                park: ParkWaker,
                dynamic: DynamicallyBoundWaker,
                hvf: HvfWaker,
            }
        }

        struct HvfWaker(u32);

        impl Waker for HvfWaker {
            fn wake(&self) {
                hvf::vcpu_request_exit(self.0).unwrap();
            }
        }

        // Create the underlying HVF vCPU.
        let mut hvf_vcpu = HvfVcpu::new(
            parker.clone(),
            self.guest_mem.clone(),
            &self.hvf_vm,
            self.vcpu_count,
            self.ht_enabled,
        )
        .expect("Can't create HVF vCPU");

        let hvf_vcpuid = hvf_vcpu.id();

        // Create and register the signal
        let signal = Arc::new(SignalChannel::new(VcpuWakerSet {
            park: ParkWaker::default(),
            dynamic: DynamicallyBoundWaker::default(),
            hvf: HvfWaker(hvf_vcpuid),
        }));
        let cpu_shutdown_task = self
            .shutdown
            .spawn_signal(
                VmmShutdownPhase::VcpuExitLoop,
                BoundSignalChannel::new(signal.clone(), VcpuSignalMask::EXIT_LOOP),
            )
            .unwrap_or_run_now();

        let hvf_destroy_task = self
            .shutdown
            .spawn_signal(
                VmmShutdownPhase::VcpuDestroy,
                BoundSignalChannel::new(signal.clone(), VcpuSignalMask::DESTROY_VM),
            )
            .unwrap_or_run_now();

        // Register the vCPU with the parker
        let mut park_task = parker.register_vcpu(signal.clone());

        // Notify init done (everything is registered)
        // TODO: We should be using startup signals for these to reduce the number of thread wake-ups.
        init_sender.send(true).unwrap();

        // Wait for boot signal
        let Ok(entry_addr) =
            signal.recv_with_cancel(VcpuSignalMask::ANY_SHUTDOWN, &self.boot_receiver)
        else {
            // Destroy both aforementioned tasks—the user has requested a shutdown.
            return;
        };

        let entry_addr = entry_addr.raw_value();

        // Create a guard for loop exit
        let shutdown_handle = VmmShutdownHandle(self.exit_evt.try_clone().unwrap());
        let vcpu_loop_exit_guard = scopeguard::guard((), |()| {
            shutdown_handle.request_shutdown();
        });

        let vcpu_loop_exit_guard = (vcpu_loop_exit_guard, cpu_shutdown_task);

        // Setup the vCPU
        hvf_vcpu
            .set_initial_state(entry_addr, self.cpu_index() != 0)
            .unwrap_or_else(|_| panic!("Can't set HVF vCPU {} initial state", hvf_vcpuid));

        // Finally, start virtualization!
        loop {
            // Handle events
            if !signal.take(VcpuSignalMask::EXIT_LOOP).is_empty() {
                break;
            }

            // (this should never happen if we haven't exited the loop yet)
            debug_assert!(signal.take(VcpuSignalMask::DESTROY_VM).is_empty());

            let Ok(park_task_tmp) = parker.process_park_commands(&*signal, park_task) else {
                error!(
                    "Thread responsible for unparking vCPUs aborted the operation; shutting down!"
                );
                break;
            };
            park_task = park_task_tmp;

            // Run emulation
            let emulation = signal.wait(VcpuSignalMask::ALL_WAIT, VcpuWakerSet::hvf, || {
                self.run_emulation(&mut hvf_vcpu)
            });

            // Handle emulation result
            let Some(emulation) = emulation else {
                // This is a VM exit, which has no side-effects.
                continue;
            };

            match emulation {
                // Emulation ran successfully, continue.
                Ok(VcpuEmulation::Handled) => (),
                // The guest was rebooted or halted.
                Ok(VcpuEmulation::Stopped) => {
                    break;
                }
                // Emulation errors lead to vCPU exit.
                Err(_) => {
                    break;
                }
            }
        }

        drop(vcpu_loop_exit_guard);

        // Now, we just have to wait for the permission to destroy our vCPUs. We can't just let these
        // become dangling handles because cause an entire list of vCPUs to exit requires all handles
        // in that list to exit successfully.
        //
        // We really don't want any other signal to shutdown the CPU. Note: `wait_on_park` only wakes
        // up if we genuinely receive the signal.
        signal.wait_on_park(VcpuSignalMask::DESTROY_VM);

        hvf_vcpu.destroy();
        drop(hvf_destroy_task);
    }
}

// Allow currently unused Pause and Exit events. These will be used by the vmm later on.
#[allow(unused)]
#[derive(Debug)]
/// List of events that the Vcpu can receive.
pub enum VcpuEvent {
    /// Pause the Vcpu.
    Pause,
    /// Event that should resume the Vcpu.
    Resume,
    // Serialize and Deserialize to follow after we get the support from kvm-ioctls.
}

/// Wrapper over Vcpu that hides the underlying interactions with the Vcpu thread.
pub struct VcpuHandle {
    pub boot_sender: Sender<GuestAddress>,
    vcpu_thread: thread::JoinHandle<()>,
}

impl VcpuHandle {
    pub fn new(boot_sender: Sender<GuestAddress>, vcpu_thread: thread::JoinHandle<()>) -> Self {
        Self {
            boot_sender,
            vcpu_thread,
        }
    }

    pub fn thread(&self) -> &Thread {
        self.vcpu_thread.thread()
    }

    pub fn boot(&self, entry_addr: GuestAddress) {
        self.boot_sender.send(entry_addr).unwrap();
    }

    pub fn join(self) {
        debug!(
            "Joining on vcpu thread: {:?}",
            self.vcpu_thread.thread().name()
        );

        let _ = self.vcpu_thread.join();
    }
}

enum VcpuEmulation {
    Handled,
    Stopped,
    #[cfg(target_arch = "aarch64")]
    WaitForEvent,
    #[cfg(target_arch = "aarch64")]
    WaitForEventExpired,
    #[cfg(target_arch = "aarch64")]
    WaitForEventTimeout(Duration),
    #[cfg(target_arch = "aarch64")]
    PvlockPark,
    #[cfg(target_arch = "aarch64")]
    PvlockUnpark(u64),
}

/*
#[cfg(test)]
#[cfg(target_arch = "aarch64")]
mod tests {
    #[cfg(target_arch = "x86_64")]
    use crossbeam_channel::{unbounded, RecvTimeoutError};
    use std::fs::File;
    #[cfg(target_arch = "x86_64")]
    use std::os::unix::io::AsRawFd;
    use std::sync::{Arc, Barrier};
    #[cfg(target_arch = "x86_64")]
    use std::time::Duration;

    use super::super::devices;
    use super::*;

    use utils::signal::validate_signal_num;

    // In tests we need to close any pending Vcpu threads on test completion.
    impl Drop for VcpuHandle {
        fn drop(&mut self) {
            // Make sure the Vcpu is out of KVM_RUN.
            self.send_event(VcpuEvent::Pause).unwrap();
            // Close the original channel so that the Vcpu thread errors and goes to exit state.
            let (event_sender, _event_receiver) = unbounded();
            self.event_sender = event_sender;
            // Wait for the Vcpu thread to finish execution
            self.vcpu_thread.take().unwrap().join().unwrap();
        }
    }

    // Auxiliary function being used throughout the tests.
    fn setup_vcpu(mem_size: usize) -> (Vm, Vcpu, GuestMemoryMmap) {
        let kvm = KvmContext::new().unwrap();
        let gm = GuestMemoryMmap::from_ranges(&[(GuestAddress(0), mem_size)]).unwrap();
        let mut vm = Vm::new(kvm.fd()).expect("Cannot create new vm");
        assert!(vm.memory_init(&gm, kvm.max_memslots()).is_ok());

        let exit_evt = EventFd::new(utils::eventfd::EFD_NONBLOCK).unwrap();

        let vcpu;
        #[cfg(any(target_arch = "x86", target_arch = "x86_64"))]
        {
            vm.setup_irqchip().unwrap();
            vcpu = Vcpu::new_x86_64(
                1,
                vm.fd(),
                vm.supported_cpuid().clone(),
                vm.supported_msrs().clone(),
                devices::Bus::new(),
                exit_evt,
                super::super::TimestampUs::default(),
            )
            .unwrap();
        }
        #[cfg(target_arch = "aarch64")]
        {
            vcpu = Vcpu::new_aarch64(1, vm.fd(), exit_evt, super::super::TimestampUs::default())
                .unwrap();
            vm.setup_irqchip(1).expect("Cannot setup irqchip");
        }

        (vm, vcpu, gm)
    }

    #[test]
    fn test_set_mmio_bus() {
        let (_, mut vcpu, _) = setup_vcpu(0x1000);
        assert!(vcpu.mmio_bus.is_none());
        vcpu.set_mmio_bus(devices::Bus::new());
        assert!(vcpu.mmio_bus.is_some());
    }

    #[test]
    #[cfg(any(target_arch = "x86", target_arch = "x86_64"))]
    fn test_get_supported_cpuid() {
        let kvm = KvmContext::new().unwrap();
        let vm = Vm::new(kvm.fd()).expect("Cannot create new vm");
        let cpuid = kvm
            .kvm
            .get_supported_cpuid(KVM_MAX_CPUID_ENTRIES)
            .expect("Cannot get supported cpuid");
        assert_eq!(vm.supported_cpuid().as_slice(), cpuid.as_slice());
    }

    #[test]
    fn test_vm_memory_init() {
        let mut kvm_context = KvmContext::new().unwrap();
        let mut vm = Vm::new(kvm_context.fd()).expect("Cannot create new vm");

        // Create valid memory region and test that the initialization is successful.
        let gm = GuestMemoryMmap::from_ranges(&[(GuestAddress(0), 0x1000)]).unwrap();
        assert!(vm.memory_init(&gm, kvm_context.max_memslots()).is_ok());

        // Set the maximum number of memory slots to 1 in KvmContext to check the error
        // path of memory_init. Create 2 non-overlapping memory slots.
        kvm_context.max_memslots = 1;
        let gm = GuestMemoryMmap::from_ranges(&[
            (GuestAddress(0x0), 0x1000),
            (GuestAddress(0x1001), 0x2000),
        ])
        .unwrap();
        assert!(vm.memory_init(&gm, kvm_context.max_memslots()).is_err());
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn test_setup_irqchip() {
        let kvm_context = KvmContext::new().unwrap();
        let vm = Vm::new(kvm_context.fd()).expect("Cannot create new vm");

        vm.setup_irqchip().expect("Cannot setup irqchip");
        // Trying to setup two irqchips will result in EEXIST error. At the moment
        // there is no good way of testing the actual error because io::Error does not implement
        // PartialEq.
        assert!(vm.setup_irqchip().is_err());

        let _vcpu = Vcpu::new_x86_64(
            1,
            vm.fd(),
            vm.supported_cpuid().clone(),
            vm.supported_msrs().clone(),
            devices::Bus::new(),
            EventFd::new(utils::eventfd::EFD_NONBLOCK).unwrap(),
            super::super::TimestampUs::default(),
        )
        .unwrap();
        // Trying to setup irqchip after KVM_VCPU_CREATE was called will result in error.
        assert!(vm.setup_irqchip().is_err());
    }

    #[cfg(target_arch = "aarch64")]
    #[test]
    fn test_setup_irqchip() {
        let kvm = KvmContext::new().unwrap();

        let mut vm = Vm::new(kvm.fd()).expect("Cannot create new vm");
        let vcpu_count = 1;
        let _vcpu = Vcpu::new_aarch64(
            1,
            vm.fd(),
            EventFd::new(utils::eventfd::EFD_NONBLOCK).unwrap(),
            super::super::TimestampUs::default(),
        )
        .unwrap();

        vm.setup_irqchip(vcpu_count).expect("Cannot setup irqchip");
        // Trying to setup two irqchips will result in EEXIST error.
        assert!(vm.setup_irqchip(vcpu_count).is_err());
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn test_configure_vcpu() {
        let (_vm, mut vcpu, vm_mem) = setup_vcpu(0x10000);

        let mut vcpu_config = VcpuConfig {
            vcpu_count: 1,
            ht_enabled: false,
            cpu_template: None,
        };

        assert!(vcpu
            .configure_x86_64(&vm_mem, GuestAddress(0), &vcpu_config)
            .is_ok());

        // Test configure while using the T2 template.
        vcpu_config.cpu_template = Some(CpuFeaturesTemplate::T2);
        assert!(vcpu
            .configure_x86_64(&vm_mem, GuestAddress(0), &vcpu_config)
            .is_ok());

        // Test configure while using the C3 template.
        vcpu_config.cpu_template = Some(CpuFeaturesTemplate::C3);
        assert!(vcpu
            .configure_x86_64(&vm_mem, GuestAddress(0), &vcpu_config)
            .is_ok());
    }

    #[cfg(target_arch = "aarch64")]
    #[test]
    fn test_configure_vcpu() {
        let kvm = KvmContext::new().unwrap();
        let gm = GuestMemoryMmap::from_ranges(&[(GuestAddress(0), 0x10000)]).unwrap();
        let mut vm = Vm::new(kvm.fd()).expect("new vm failed");
        assert!(vm.memory_init(&gm, kvm.max_memslots()).is_ok());

        // Try it for when vcpu id is 0.
        let mut vcpu = Vcpu::new_aarch64(
            0,
            vm.fd(),
            EventFd::new(utils::eventfd::EFD_NONBLOCK).unwrap(),
            super::super::TimestampUs::default(),
        )
        .unwrap();

        assert!(vcpu
            .configure_aarch64(vm.fd(), &gm, GuestAddress(0))
            .is_ok());

        // Try it for when vcpu id is NOT 0.
        let mut vcpu = Vcpu::new_aarch64(
            1,
            vm.fd(),
            EventFd::new(utils::eventfd::EFD_NONBLOCK).unwrap(),
            super::super::TimestampUs::default(),
        )
        .unwrap();

        assert!(vcpu
            .configure_aarch64(vm.fd(), &gm, GuestAddress(0))
            .is_ok());
    }

    #[test]
    fn test_kvm_context() {
        use std::os::unix::fs::MetadataExt;
        use std::os::unix::io::{AsRawFd, FromRawFd};

        let c = KvmContext::new().unwrap();

        assert!(c.max_memslots >= 32);

        let kvm = Kvm::new().unwrap();
        let f = unsafe { File::from_raw_fd(kvm.as_raw_fd()) };
        let m1 = f.metadata().unwrap();
        let m2 = File::open("/dev/kvm").unwrap().metadata().unwrap();

        assert_eq!(m1.dev(), m2.dev());
        assert_eq!(m1.ino(), m2.ino());
    }

    #[test]
    fn test_vcpu_tls() {
        let (_, mut vcpu, _) = setup_vcpu(0x1000);

        // Running on the TLS vcpu should fail before we actually initialize it.
        unsafe {
            assert!(Vcpu::run_on_thread_local(|_| ()).is_err());
        }

        // Initialize vcpu TLS.
        vcpu.init_thread_local_data().unwrap();

        // Validate TLS vcpu is the local vcpu by changing the `id` then validating against
        // the one in TLS.
        vcpu.id = 12;
        unsafe {
            assert!(Vcpu::run_on_thread_local(|v| assert_eq!(v.id, 12)).is_ok());
        }

        // Reset vcpu TLS.
        assert!(vcpu.reset_thread_local_data().is_ok());

        // Running on the TLS vcpu after TLS reset should fail.
        unsafe {
            assert!(Vcpu::run_on_thread_local(|_| ()).is_err());
        }

        // Second reset should return error.
        assert!(vcpu.reset_thread_local_data().is_err());
    }

    #[test]
    fn test_invalid_tls() {
        let (_, mut vcpu, _) = setup_vcpu(0x1000);
        // Initialize vcpu TLS.
        vcpu.init_thread_local_data().unwrap();
        // Trying to initialize non-empty TLS should error.
        vcpu.init_thread_local_data().unwrap_err();
    }

    #[test]
    fn test_vcpu_kick() {
        Vcpu::register_kick_signal_handler();
        let (vm, mut vcpu, _mem) = setup_vcpu(0x1000);

        let kvm_run =
            KvmRunWrapper::mmap_from_fd(&vcpu.fd, vm.fd.run_size()).expect("cannot mmap kvm-run");
        let success = Arc::new(std::sync::atomic::AtomicBool::new(false));
        let vcpu_success = success.clone();
        let barrier = Arc::new(Barrier::new(2));
        let vcpu_barrier = barrier.clone();
        // Start Vcpu thread which will be kicked with a signal.
        let handle = std::thread::Builder::new()
            .name("test_vcpu_kick".to_string())
            .spawn(move || {
                vcpu.init_thread_local_data().unwrap();
                // Notify TLS was populated.
                vcpu_barrier.wait();
                // Loop for max 1 second to check if the signal handler has run.
                for _ in 0..10 {
                    if kvm_run.as_mut_ref().immediate_exit == 1 {
                        // Signal handler has run and set immediate_exit to 1.
                        vcpu_success.store(true, Ordering::Release);
                        break;
                    }
                    std::thread::sleep(std::time::Duration::from_millis(100));
                }
            })
            .expect("cannot start thread");

        // Wait for the vcpu to initialize its TLS.
        barrier.wait();
        // Kick the Vcpu using the custom signal.
        handle
            .kill(sigrtmin() + VCPU_RTSIG_OFFSET)
            .expect("failed to signal thread");
        handle.join().expect("failed to join thread");
        // Verify that the Vcpu saw its kvm immediate-exit as set.
        assert!(success.load(Ordering::Acquire));
    }

    #[cfg(target_arch = "x86_64")]
    // Sends an event to a vcpu and expects a particular response.
    fn queue_event_expect_response(handle: &VcpuHandle, event: VcpuEvent, response: VcpuResponse) {
        handle
            .send_event(event)
            .expect("failed to send event to vcpu");
        assert_eq!(
            handle
                .response_receiver()
                .recv_timeout(Duration::from_millis(100))
                .expect("did not receive event response from vcpu"),
            response
        );
    }

    #[cfg(target_arch = "x86_64")]
    // Sends an event to a vcpu and expects no response.
    fn queue_event_expect_timeout(handle: &VcpuHandle, event: VcpuEvent) {
        handle
            .send_event(event)
            .expect("failed to send event to vcpu");
        assert_eq!(
            handle
                .response_receiver()
                .recv_timeout(Duration::from_millis(100)),
            Err(RecvTimeoutError::Timeout)
        );
    }

    #[test]
    fn test_vcpu_rtsig_offset() {
        assert!(validate_signal_num(sigrtmin() + VCPU_RTSIG_OFFSET).is_ok());
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn test_vm_save_restore_state() {
        let kvm_fd = Kvm::new().unwrap();
        let vm = Vm::new(&kvm_fd).expect("new vm failed");
        // Irqchips, clock and pitstate are not configured so trying to save state should fail.
        assert!(vm.save_state().is_err());

        let (vm, _, _mem) = setup_vcpu(0x1000);
        let vm_state = vm.save_state().unwrap();
        assert_eq!(
            vm_state.pitstate.flags | KVM_PIT_SPEAKER_DUMMY,
            KVM_PIT_SPEAKER_DUMMY
        );
        assert_eq!(vm_state.clock.flags & KVM_CLOCK_TSC_STABLE, 0);
        assert_eq!(vm_state.pic_master.chip_id, KVM_IRQCHIP_PIC_MASTER);
        assert_eq!(vm_state.pic_slave.chip_id, KVM_IRQCHIP_PIC_SLAVE);
        assert_eq!(vm_state.ioapic.chip_id, KVM_IRQCHIP_IOAPIC);

        let (vm, _, _mem) = setup_vcpu(0x1000);
        assert!(vm.restore_state(&vm_state).is_ok());
    }

    #[cfg(target_arch = "x86_64")]
    #[test]
    fn test_vcpu_save_restore_state() {
        let (_vm, vcpu, _mem) = setup_vcpu(0x1000);
        let state = vcpu.save_state();
        assert!(state.is_ok());
        assert!(vcpu.restore_state(state.unwrap()).is_ok());

        unsafe { libc::close(vcpu.fd.as_raw_fd()) };
        let state = VcpuState {
            cpuid: CpuId::new(1),
            msrs: Msrs::new(1),
            debug_regs: Default::default(),
            lapic: Default::default(),
            mp_state: Default::default(),
            regs: Default::default(),
            sregs: Default::default(),
            vcpu_events: Default::default(),
            xcrs: Default::default(),
            xsave: Default::default(),
        };
        // Setting default state should always fail.
        assert!(vcpu.restore_state(state).is_err());
    }
}
*/
