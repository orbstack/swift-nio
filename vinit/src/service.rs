use std::{collections::{BTreeMap}, process::Command, fs, fmt::{Display, Formatter}, time::Duration, error::Error};

use nix::{sys::signal::{Signal}};
use once_cell::sync::Lazy;
use tokio::sync::{Mutex};

use crate::{pidfd::PidFd, InitError};

pub static PROCESS_WAIT_LOCK: Lazy<Mutex<()>> = Lazy::new(|| Mutex::new(()));

const OOM_SCORE_CRITICAL: i32 = -950;
const RESTART_DELAY: u64 = 3;

#[derive(Eq, PartialEq, PartialOrd, Ord, Clone, Copy, Debug)]
pub struct Service {
    pub name: &'static str,
    pub critical: bool,
    pub restartable: bool,
    pub clean_stop: bool,
}

impl Service {
    pub const CHRONY: Service = Service {
        // vague service names to avoid revealing info
        name: "time-sync",
        critical: true,
        restartable: true,
        clean_stop: false,
    };
    pub const UDEV: Service = Service {
        name: "udev",
        critical: true,
        restartable: true,
        clean_stop: false,
    };
    pub const SCON: Service = Service {
        name: "scon",
        critical: true,
        restartable: false,
        clean_stop: true,
    };

    pub const SSH: Service = Service {
        name: "ssh",
        critical: true,
        restartable: true,
        clean_stop: false,
    };

    pub const FUSE_PASSTHROUGH: Service = Service {
        name: "pfs",
        critical: false,
        restartable: true,
        clean_stop: false,
    };

    // critical because macOS NFS can freeze, or even worse, kernel panic if it crashes
    // nfs4 still needs this - kernel nfsd talks to it
    pub const NFS_MOUNTD: Service = Service {
        name: "mountd",
        critical: true,
        restartable: true,
        clean_stop: false,
    };

    pub const MERGERFS_IMAGES: Service = Service {
        name: "images-fs",
        critical: false,
        restartable: true,
        clean_stop: false,
    };

    pub const MERGERFS_CONTAINERS: Service = Service {
        name: "containers-fs",
        critical: false,
        restartable: true,
        clean_stop: false,
    };
}

struct CommandSpec {
    exe: String,
    args: Vec<String>,
}

impl Display for Service {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.name)
    }
}

pub struct ServiceTracker {
    pids: BTreeMap<u32, Service>,
    command_specs: BTreeMap<Service, CommandSpec>,
    pub shutting_down: bool,
}

impl ServiceTracker {
    pub fn new() -> Self {
        Self {
            pids: BTreeMap::new(),
            command_specs: BTreeMap::new(),
            shutting_down: false,
        }
    }

    pub fn spawn(&mut self, service: Service, cmd: &mut Command) -> Result<(), Box<dyn Error>> {
        let pid = cmd.spawn()
            .map_err(|e| InitError::SpawnService { service, error: e })?
            .id();

        // set OOM score adj for critical services
        if service.critical {
            fs::write(format!("/proc/{}/oom_score_adj", pid),
                format!("{}", OOM_SCORE_CRITICAL))?;
        }

        self.pids.insert(pid, service);
        // for restarting
        self.command_specs.insert(service, CommandSpec {
            exe: cmd.get_program().to_string_lossy().to_string(),
            args: cmd.get_args().into_iter().map(|s| s.to_string_lossy().to_string()).collect(),
        });
        Ok(())
    }
    
    pub async fn restart(&mut self, service: Service) -> Result<(), Box<dyn Error>> {
        if !service.restartable || self.shutting_down {
            return Ok(());
        }

        // delay
        tokio::time::sleep(Duration::from_secs(RESTART_DELAY)).await;

        let spec = self.command_specs.get(&service).unwrap();
        self.spawn(service, &mut Command::new(&spec.exe)
            .args(&spec.args))
    }

    pub fn stop_for_shutdown(&mut self, signal: Signal) -> std::io::Result<Vec<PidFd>> {
        let mut pidfds = Vec::new();
        for (pid, service) in self.pids.iter() {
            if service.clean_stop {
                let pidfd = PidFd::open(*pid as i32)?;
                pidfd.kill(signal)?;
                pidfds.push(pidfd);
            }
        }

        self.shutting_down = true;
        Ok(pidfds)
    }

    pub fn on_pid_exit(&mut self, pid: u32) -> Option<Service> {
        if let Some(service) = self.pids.remove(&pid) {
            Some(service)
        } else {
            None
        }
    }
}
