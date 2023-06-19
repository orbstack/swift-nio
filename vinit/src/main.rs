use std::{error::Error, fs::{self}, time::{Instant}, process::{ExitStatus}, collections::BTreeMap, sync::Arc};

use nix::{sys::{wait::{waitpid, WaitPidFlag, WaitStatus}}, unistd::{Pid, getpid}, errno::Errno};

mod helpers;
use service::{PROCESS_WAIT_LOCK, ServiceTracker};
use tokio::{signal::unix::{signal, SignalKind}, sync::{Mutex, mpsc::{self, Sender}}};

mod action;
use action::SystemAction;
mod vcontrol;
mod service;
mod blockdev;
mod ethtool;
mod loopback;
mod pidfd;
mod startup;
mod shutdown;

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
    Mount { source: String, dest: String, #[source] error: nix::Error },
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
}

#[derive(Clone)]
struct SystemInfo {
    kernel_version: String,
    cmdline: Vec<String>,
    seed_configs: BTreeMap<String, String>,
}

impl SystemInfo {
    fn read() -> Result<SystemInfo, Box<dyn Error>> {
        // trim newline
        let kernel_version = fs::read_to_string("/proc/sys/kernel/osrelease")?.trim().to_string();
        let cmdline: Vec<_> = fs::read_to_string("/proc/cmdline")?.trim()
            .split(' ')
            .map(|s| s.to_string())
            .collect();
        let seed_configs = cmdline
            .iter()
            .filter(|s| s.starts_with("orb."))
            .map(|s| {
                let mut parts = s.splitn(2, '=');
                let key = parts.next().unwrap().strip_prefix("orb.").unwrap().to_string();
                let value = parts.next().unwrap_or("").to_string();
                (key, value)
            })
            .collect();
    
        Ok(SystemInfo {
            kernel_version,
            cmdline,
            seed_configs,
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

async fn reap_children(service_tracker: Arc<Mutex<ServiceTracker>>, action_tx: Sender<SystemAction>) -> Result<(), Box<dyn Error>> {
    loop {
        let _guard = PROCESS_WAIT_LOCK.lock().await;
        let wstatus = match waitpid(None, Some(WaitPidFlag::WNOHANG)) {
            Ok(wstatus) => wstatus,
            Err(Errno::ECHILD) => {
                // no children
                break;
            },
            Err(Errno::EINTR) => {
                // interrupted by signal
                continue;
            },
            Err(e) => {
                return Err(InitError::Waitpid(e).into());
            },
        };
        let mut service_tracker = service_tracker.lock().await;
        match wstatus {
            WaitStatus::Exited(pid, status) => {
                if let Some(service) = service_tracker.on_pid_exit(pid.as_raw() as u32) {
                    if service.restartable && !service_tracker.shutting_down {
                        // restart the service
                        println!("  !  Service {} exited: status {}, restarting", service, status);
                        service_tracker.restart(service).await?;
                    } else if service.critical && !service_tracker.shutting_down {
                        // service is critical and not restartable!
                        // shut down immediately
                        println!("  !  Critical service {} exited: shutting down", service);
                        action_tx.send(SystemAction::Shutdown).await?;
                    } else {
                        println!("  !  Service {} exited: status {}", service, status);
                    }
                } else {
                    println!("  !  Untracked process {} exited: status {}", pid, status);
                }
            },
            WaitStatus::Signaled(pid, signal, _) => {
                if let Some(service) = service_tracker.on_pid_exit(pid.as_raw() as u32) {
                    // don't restart on kill. kill must be intentional
                    println!("  !  Service {} exited: signal {}", service, signal);
                } else {
                    println!("  !  Untracked process {} exited: signal {}", pid, signal);
                }
            },
            _ => {
                break;
            },
        }
    }

    Ok(())
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
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
            reap_children(service_tracker_clone.clone(), action_tx_clone.clone()).await.unwrap();
        }
    });

    // listen for poweroff requests (SIGUSR2)
    let mut sigusr2_stream = signal(SignalKind::user_defined2())?;
    let action_tx_clone = action_tx.clone();
    tokio::spawn(async move {
        loop {
            sigusr2_stream.recv().await;
            println!("  -  Received poweroff request");
            action_tx_clone.send(SystemAction::Shutdown).await.unwrap();
        }
    });

    // wait for action on mpsc
    loop {
        match action_rx.recv().await {
            Some(action) => {
                match action {
                    SystemAction::Shutdown => {
                        println!("  -  Shutting down");
                        break;
                    },
                }
            },
            None => {
                println!("  -  Channel closed");
                break;
            },
        }
    }

    // close channel to avoid hang in ServiceTracker lock (in reap_children)
    drop(action_rx);
    // proceed with shutdown
    shutdown::main(service_tracker.clone()).await?;

    Ok(())
}
