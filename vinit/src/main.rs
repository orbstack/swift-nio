use std::{process::ExitStatus, sync::Arc, time::Instant};

use anyhow::anyhow;
use base64::Engine;
use filesystem::HostDiskStats;
use nix::{
    errno::Errno,
    sys::{
        reboot::{reboot, RebootMode},
        utsname::uname,
        wait::{waitpid, WaitPidFlag, WaitStatus},
    },
    unistd::{getpid, Pid},
};

mod helpers;
use bincode::Decode;
use service::{Service, ServiceTracker, PROCESS_WAIT_LOCK};
use tokio::{
    signal::unix::{signal, SignalKind},
    sync::{
        mpsc::{self, Sender},
        Mutex,
    },
};

mod action;
use action::SystemAction;
use tracing::debug;
mod blockdev;
mod btrfs;
mod filesystem;
mod loopback;
mod memory;
mod pidfd;
#[cfg(target_arch = "aarch64")]
mod rosetta;
mod service;
mod shutdown;
mod startup;
mod vcontrol;

// debug flag
#[cfg(debug_assertions)]
static DEBUG: bool = true;
#[cfg(not(debug_assertions))]
static DEBUG: bool = false;

#[derive(thiserror::Error, Debug)]
pub enum InitError {
    #[error("not pid 1")]
    NotPid1,
    #[error("failed to mount {} to {}: {}", .source, .dest, .error)]
    Mount {
        source: String,
        dest: String,
        #[source]
        error: nix::Error,
    },
    #[error("failed to resize data filesystem: {}", .0)]
    ResizeDataFs(ExitStatus),
    #[error("failed to waitpid: {}", .0)]
    Waitpid(nix::Error),
    #[error("failed to kill: {}", .0)]
    Kill(nix::Error),
    #[error("timeout")]
    Timeout,
    #[error("failed to poll pidfd: {}", .0)]
    PollPidFd(tokio::io::Error),
    #[error("failed to get time from ntp: {:?}", .0)]
    NtpGetTime(sntpc::Error),
    #[error("failed to parse proc stat for pid {}", .0)]
    ParseProcStat(i32),
    #[error("failed to spawn service {}: {}", .service, .error)]
    SpawnService {
        service: Service,
        #[source]
        error: std::io::Error,
    },
    #[error("missing data partition: {}", .0)]
    MissingDataPartition(#[from] anyhow::Error),
    #[error("invalid elf")]
    InvalidElf,
}

#[derive(Clone)]
struct SystemInfo {
    kernel_version: String,
    seed: SeedData,
}

#[derive(Debug, Clone, Decode)]
struct SeedData {
    data_size_mib: u64,
    initial_disk_stats: HostDiskStats,

    host_major_version: u16,
    host_build_version: String,

    console_path: String,
    console_is_pipe: bool,
}

impl SystemInfo {
    fn read() -> anyhow::Result<SystemInfo> {
        let uname = uname()?;
        let seed_str = std::env::var("ORB_S").map_err(|_| anyhow!("missing seed data"))?;
        let seed_bytes = base64::engine::general_purpose::URL_SAFE_NO_PAD.decode(seed_str)?;
        let (seed, _) = bincode::decode_from_slice(&seed_bytes, bincode::config::legacy())?;

        Ok(SystemInfo {
            kernel_version: uname.release().to_string_lossy().to_string(),
            seed,
        })
    }
}

struct Timeline {
    last_stage_start: Instant,
}

impl Timeline {
    fn new() -> Timeline {
        Timeline {
            last_stage_start: Instant::now(),
        }
    }

    fn begin(&mut self, stage: &str) {
        let now = Instant::now();
        let diff = now.duration_since(self.last_stage_start);
        println!(" [*] {}  (+{}ms)", stage, diff.as_millis());
        self.last_stage_start = now;
    }
}

async fn reap_children(
    service_tracker: Arc<Mutex<ServiceTracker>>,
    action_tx: Sender<SystemAction>,
) -> anyhow::Result<()> {
    loop {
        let _guard = PROCESS_WAIT_LOCK.lock().await;
        let wstatus = match waitpid(None, Some(WaitPidFlag::WNOHANG)) {
            Ok(wstatus) => wstatus,
            Err(Errno::ECHILD) => {
                // no children
                break;
            }
            Err(Errno::EINTR) => {
                // interrupted by signal
                continue;
            }
            Err(e) => {
                return Err(InitError::Waitpid(e).into());
            }
        };
        let mut service_tracker = service_tracker.lock().await;
        match wstatus {
            WaitStatus::Exited(pid, status) => {
                if let Some(service) = service_tracker.on_pid_exit(pid.as_raw() as u32) {
                    if service.restartable && !service_tracker.shutting_down {
                        // restart the service
                        println!(
                            "  !  Service {} exited: status {}, restarting",
                            service, status
                        );
                        service_tracker.restart(service).await?;
                    } else if service.critical && !service_tracker.shutting_down && !DEBUG {
                        // service is critical and not restartable!
                        // shut down immediately; in debug, allow manually replacing scon
                        println!("  !  Critical service {} exited: shutting down", service);
                        action_tx.send(SystemAction::Shutdown).await?;
                    } else {
                        println!("  !  Service {} exited: status {}", service, status);
                    }
                } else {
                    debug!("  !  Untracked process {} exited: status {}", pid, status);
                }
            }
            WaitStatus::Signaled(pid, signal, _) => {
                if let Some(service) = service_tracker.on_pid_exit(pid.as_raw() as u32) {
                    // don't restart on kill. kill must be intentional
                    println!("  !  Service {} exited: signal {}", service, signal);
                } else {
                    debug!("  !  Untracked process {} exited: signal {}", pid, signal);
                }
            }
            _ => {
                break;
            }
        }
    }

    Ok(())
}

async fn main_wrapped() -> anyhow::Result<()> {
    if getpid() != Pid::from_raw(1) {
        return Err(InitError::NotPid1.into());
    }

    let (action_tx, mut action_rx) = mpsc::channel::<SystemAction>(1);
    let service_tracker = Arc::new(Mutex::new(ServiceTracker::new()));

    // boot the system!
    startup::main(service_tracker.clone(), action_tx.clone()).await?;

    // reap children, orphans, and zombies
    let mut sigchld_stream = signal(SignalKind::child())?;
    let action_tx_clone = action_tx.clone();
    let service_tracker_clone = service_tracker.clone();
    tokio::spawn(async move {
        loop {
            sigchld_stream.recv().await;
            reap_children(service_tracker_clone.clone(), action_tx_clone.clone())
                .await
                .unwrap();
        }
    });

    // listen for poweroff requests (SIGUSR2)
    let mut sigusr2_stream = signal(SignalKind::user_defined2())?;
    let action_tx_clone = action_tx.clone();
    tokio::spawn(async move {
        loop {
            sigusr2_stream.recv().await;
            println!("  -  Received poweroff request");
            // ignore send error: already shutting down = action channel closed
            let _ = action_tx_clone.send(SystemAction::Shutdown).await;
        }
    });

    // wait for action on mpsc
    // https://github.com/orbstack/macvirt/pull/107#discussion_r1623682202
    #[allow(clippy::never_loop)]
    loop {
        match action_rx.recv().await {
            Some(action) => match action {
                SystemAction::Shutdown => {
                    println!("  -  Shutting down");
                    break;
                }
            },
            None => {
                println!("  -  Channel closed");
                break;
            }
        }
    }

    // close channel to avoid hang in ServiceTracker lock (in reap_children)
    drop(action_rx);
    // proceed with shutdown
    shutdown::main(service_tracker.clone()).await?;

    Ok(())
}

#[tokio::main]
async fn main() {
    let result = main_wrapped().await;
    if let Err(e) = result {
        println!(" !!! Init failed: {}", e);
        reboot(RebootMode::RB_POWER_OFF).unwrap();
        std::process::exit(1);
    }
}
