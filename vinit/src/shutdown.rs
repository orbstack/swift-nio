use std::{error::Error, fs::{self, DirEntry}, time::Duration, os::{fd::{AsRawFd}}, io::{self}, sync::Arc, path::Path};

use nix::{sys::{signal::{kill, Signal}, reboot::{reboot, RebootMode}}, mount::{umount2, MntFlags}, unistd::{Pid, self}, errno::Errno, fcntl::readlink};

use crate::pidfd::PidFd;
use crate::service::{PROCESS_WAIT_LOCK, ServiceTracker};
use tokio::{sync::{Mutex}};

use crate::{loopback, Timeline, InitError, DEBUG};

// only includes root namespace
const UNMOUNT_ITERATION_LIMIT: usize = 10;
const DATA_FILESYSTEM_TYPES: &[&str] = &[
    "virtiofs",
    "btrfs",
];

const SERVICE_SIGTERM_TIMEOUT: Duration = Duration::from_secs(20);
const PROCESS_SIGKILL_TIMEOUT: Duration = Duration::from_secs(5);

fn kill_one_entry(entry: Result<DirEntry, io::Error>, signal: Signal) -> Result<Option<PidFd>, Box<dyn Error>> {
    let filename = entry?.file_name();
    if let Ok(pid) =  filename.to_str().unwrap().parse::<i32>() {
        // skip pid 1
        if pid == 1 {
            return Ok(None);
        }
        
        // skip kthreads - they have no executable
        match readlink(Path::new(&format!("/proc/{}/exe", pid))) {
            Err(Errno::ENOENT) => {
                return Ok(None);
            },
            _ => {},
        }

        // open a pidfd before killing, then use the pidfd to kill it, and make it pollable
        let pidfd = PidFd::open(pid)?;
        pidfd.kill(signal)?;
        Ok(Some(pidfd))
    } else {
        Ok(None)
    }
}

fn broadcast_signal(signal: Signal) -> nix::Result<Vec<PidFd>> {
    // freeze to get consistent snapshot and avoid thrashing
    kill(Pid::from_raw(-1), Signal::SIGSTOP)?;

    // can't use kill(-1) because we need to know which PIDs to wait for exit
    // otherwise unmount returns EBUSY
    let mut pids = Vec::new();
    match fs::read_dir("/proc") {
        Ok(entries) => {
            for entry in entries {
                match kill_one_entry(entry, signal) {
                    Ok(Some(pid)) => {
                        pids.push(pid);
                    },
                    Err(e) => {
                        println!(" !!! Failed to read /proc entry: {}", e);
                    },
                    _ => {},
                }
            }
        },
        Err(e) => {
            println!(" !!! Failed to read /proc: {}", e);
        },
    }

    // always make sure to unfreeze
    kill(Pid::from_raw(-1), Signal::SIGCONT)?;
    Ok(pids)
}

fn unmount_one_loopback(entry: Result<DirEntry, io::Error>) -> Result<bool, Box<dyn Error>> {
    let filename = entry?.file_name();
    let bdev = filename.to_str().unwrap();
    if bdev.starts_with("loop") {
        // check if it has a loop/backing_file
        if Path::new(&format!("/sys/block/{}/loop/backing_file", bdev)).exists() {
            // ioctl LOOP_CLR_FD
            let fd = fs::OpenOptions::new()
                .read(true)
                .write(true)
                .open(format!("/dev/{}", bdev))?;
            loopback::clear_fd(fd.as_raw_fd())?;

            return Ok(true);
        }
    }

    Ok(false)
}

fn unmount_all_loopback() -> Result<bool, Box<dyn Error>> {
    let mut made_progress = false;

    // loopback
    let bdevs = fs::read_dir("/sys/block")?;
    for entry in bdevs {
        match unmount_one_loopback(entry) {
            Ok(true) => {
                made_progress = true;
            },
            Err(e) => {
                println!(" !!! Failed to unmount loopback: {}", e);
            },
            _ => {},
        }
    }

    Ok(made_progress)
}

fn unmount_all_filesystems() -> Result<bool, Box<dyn Error>> {
    let mut made_progress = false;

    // filesystems
    let mounts = fs::read_to_string("/proc/mounts")?;
    // unmount in reverse order - more likely to succeed
    for line in mounts.lines().rev() {
        let mut parts = line.split_whitespace();
        let _ = parts.next().unwrap();
        let target = parts.next().unwrap();
        let fstype = parts.next().unwrap();

        // only unmount data filesystems
        if DATA_FILESYSTEM_TYPES.contains(&fstype) {
            // unmount
            println!("  -  Unmounting {}", target);
            // TODO: MNT_DETACH?
            if let Err(e) = umount2(target, MntFlags::MNT_FORCE) {
                println!(" !!! Failed to unmount {}: {}", target, e);
            } else {
                made_progress = true;
            }
        }
    }

    Ok(made_progress)
}

fn unmount_all_round() -> Result<bool, Box<dyn Error>> {
    let mut made_progress = false;

    // loop
    if unmount_all_loopback()? {
        made_progress = true;
    }

    // filesystems (data only, to save time)
    if unmount_all_filesystems()? {
        made_progress = true;
    }

    Ok(made_progress)
}

async fn stop_nfs() -> Result<(), Box<dyn Error>> {
    let _guard = PROCESS_WAIT_LOCK.lock().await;

    tokio::process::Command::new("/opt/pkg/exportfs")
        .arg("-uav")
        .status()
        .await?;
    tokio::process::Command::new("/opt/pkg/rpc.nfsd")
        .arg("0")
        .status()
        .await?;

    Ok(())
}

async fn wait_for_pidfds_exit(pidfds: Vec<PidFd>, timeout: Duration) -> Result<(), Box<dyn Error>> {
    let futures = pidfds.into_iter()
        .map(|pidfd| {
            async move {
                let _guard = pidfd.wait().await?;
                Ok::<(), tokio::io::Error>(())
            }
        })
        .collect::<Vec<_>>();

    let results = tokio::time::timeout(timeout, futures::future::join_all(futures)).await?;
    for result in results {
        if let Err(err) = result {
            return Err(InitError::PollPidFd(err).into());
        }
    }

    Ok(())
}

pub async fn main(service_tracker: Arc<Mutex<ServiceTracker>>) -> Result<(), Box<dyn Error>> {
    let mut timeline = Timeline::new();
    timeline.begin("Shutting down");

    // disable core dump to avoid slow kills
    fs::write("/proc/sys/kernel/core_pattern", "|/bin/false")?;

    // kill services that need clean shutdown
    timeline.begin("Stop services");
    let service_pids = service_tracker.lock().await.stop_for_shutdown(Signal::SIGTERM)
        .unwrap_or_else(|e| {
            eprintln!(" !!! Failed to stop service: {}", e);
            vec![]
        });

    // stop NFS
    // rpc.mountd will be killed below
    timeline.begin("Stop NFS");
    stop_nfs().await
        .unwrap_or_else(|e| {
            eprintln!(" !!! Failed to stop NFS: {}", e);
            ()
        });

    // wait for the services to exit
    timeline.begin("Wait for services to exit");
    wait_for_pidfds_exit(service_pids, SERVICE_SIGTERM_TIMEOUT).await
        .unwrap_or_else(|e| {
            eprintln!(" !!! Failed to wait for services to exit: {}", e);
            ()
        });

    // kill all processes (these don't need clean shutdown)
    timeline.begin("Kill all processes");
    let all_pids = broadcast_signal(Signal::SIGKILL)
        .unwrap_or_else(|e| {
            eprintln!(" !!! Failed to kill all processes: {}", e);
            vec![]
        });
    wait_for_pidfds_exit(all_pids, PROCESS_SIGKILL_TIMEOUT).await
        .unwrap_or_else(|e| {
            eprintln!(" !!! Failed to wait for processes to exit: {}", e);
            ()
        });

    // remove binfmts
    // in case user added custom binfmts from data with F (open file) flag
    fs::write("/proc/sys/fs/binfmt_misc/status", "-1")?;

    // unmount loop and data filesystems, which means virtiofs and btrfs
    // we don't need to worry about tmpfs, etc.
    // so to speed up shutdown, we only umount anything that has to do with /dev/vd*
    // and we don't support device-mapper (dm) or md
    timeline.begin("Unmount filesystems");
    let mut i = 0;
    loop {
        println!("  [round {}]", i + 1);
        let made_progress = unmount_all_round()
            .unwrap_or_else(|e| {
                eprintln!(" !!! Failed to unmount filesystems: {}", e);
                false
            });
        if !made_progress {
            break;
        }

        i += 1;
        if i > UNMOUNT_ITERATION_LIMIT {
            println!("  -  Giving up");
            break;
        }
    }

    if DEBUG {
        let mounts = fs::read_to_string("/proc/mounts")?;
        println!("\nEnding with mounts:\n{}\n", mounts);

        println!("\nEnding with processes:");
        let _guard = PROCESS_WAIT_LOCK.lock().await;
        tokio::process::Command::new("/bin/ps")
            .arg("awux")
            .status()
            .await?;
        println!();

        println!("\nEnding with fds:");
        tokio::process::Command::new("/bin/ls")
            .arg("-l")
            .arg("/proc/1/fd")
            .status()
            .await?;
        println!();
    }

    // sync
    timeline.begin("Sync data");
    unistd::sync();

    // power off
    timeline.begin("Power off");
    reboot(RebootMode::RB_POWER_OFF)?;

    Ok(())
}
