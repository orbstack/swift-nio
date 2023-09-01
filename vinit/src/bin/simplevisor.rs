use std::{error::Error, process::Command, fs, os::unix::fs::symlink};

use nix::{sys::signal::{kill, Signal}, unistd::Pid};
use signal_hook::iterator::Signals;
use serde::Deserialize;

struct MonitoredChild {
    process: std::process::Child,
    args: Vec<String>,
}

#[derive(Deserialize)]
struct SimplevisorConfig {
    init_commands: Vec<Vec<String>>,
    services: Vec<Vec<String>>,
}

/*
fn init_system() -> Result<(), Box<dyn Error>> {
    // move processes to fix delegation
    fs::create_dir_all("/sys/fs/cgroup/init.scope")?;
    let all_procs = fs::read_to_string("/sys/fs/cgroup/cgroup.procs")?;
    fs::write("/sys/fs/cgroup/init.scope/cgroup.procs", all_procs)?;

    // enable all controllers for subgroups
    let subtree_controllers = fs::read_to_string("/sys/fs/cgroup/cgroup.controllers")?
        .trim()
        .split(' ')
        // prepend '+' to each controller
        .map(|s| "+".to_string() + s)
        .collect::<Vec<String>>()
        .join(" ");
    fs::write("/sys/fs/cgroup/cgroup.subtree_control", subtree_controllers)?;

    // make / rshared
    // TODO do this in rust. too dangerous though
    let status = Command::new("mount").args(&["--make-rshared", "/"])
        .status()?;
    if !status.success() {
        return Err("failed to make / rshared".into());
    }

    // symlink sockets for docker desktop compat
    fs::create_dir_all("/run/host-services")?;
    symlink("/opt/orbstack-guest/run/host-ssh-agent.sock", "/run/host-services/ssh-auth.sock")?;

    Ok(())
}
*/

// EXTREMELY simple process supervisor:
// - start processes
// - listen for all signals and forward them to children
//   * except on SIGUSR2: kill children, wait for exit, then restart
// - when any process exits, exit with the same exit code
//
// we keep tini around for signal forwarding
fn main() -> Result<(), Box<dyn Error>> {
    // get config from env
    let config_str = std::env::var("SIMPLEVISOR_CONFIG")?;
    let config: SimplevisorConfig = serde_json::from_str(&config_str)?;

    // broken: EINVAL
    //init_system()?;

    // run init commands
    for init_command in config.init_commands.iter() {
        let status = Command::new(&init_command[0])
            .args(&init_command[1..])
            .status()?;
        if !status.success() {
            return Err(format!("init command {:?} failed with {}", init_command, status).into());
        }
    }

    let mut children = config.services.iter()
        .map(|child_args| {
            let process = Command::new(&child_args[0])
                .args(&child_args[1..])
                .spawn()
                .unwrap();
            MonitoredChild {
                process,
                args: child_args.clone(),
            }
        })
        .collect::<Vec<_>>();
    let mut is_shutting_down = false;

    // remove config from env
    std::env::remove_var("SIMPLEVISOR_CONFIG");

    // forward all signals to children
    let mut signals = Signals::new(&[
        signal_hook::consts::SIGTERM,
        signal_hook::consts::SIGINT,
        signal_hook::consts::SIGQUIT,
        signal_hook::consts::SIGABRT,
        signal_hook::consts::SIGUSR1,
        signal_hook::consts::SIGUSR2,
        signal_hook::consts::SIGCONT,
        signal_hook::consts::SIGTSTP,
        signal_hook::consts::SIGTTIN,
        signal_hook::consts::SIGTTOU,
        signal_hook::consts::SIGPIPE,
        signal_hook::consts::SIGALRM,
        signal_hook::consts::SIGCHLD,
        signal_hook::consts::SIGWINCH,
        signal_hook::consts::SIGIO,
        signal_hook::consts::SIGURG,
        signal_hook::consts::SIGSYS,
        signal_hook::consts::SIGXCPU,
        signal_hook::consts::SIGXFSZ,
        signal_hook::consts::SIGVTALRM,
        signal_hook::consts::SIGPROF,
    ])?;
    for signal in signals.forever() {
        match signal {
            signal_hook::consts::SIGCHLD => {
                // a child exited?
                for (i, child) in children.iter_mut().enumerate() {
                    if let Some(status) = child.process.try_wait()? {
                        // we exit as soon as a child does
                        // this covers cases of dockerd and k8s crashing
                        // but on orderly shutdown (SIGTERM) we want to wait for dockerd. k8s shuts down faster
                        println!(" [*] service {} exited with {}", i, status);
                        if !is_shutting_down || i == 0 {
                            std::process::exit(status.code().unwrap_or(1));
                        }
                    }
                }
            }
            signal_hook::consts::SIGUSR2 => {
                // kill children, wait for exit, then restart
                for (i, child) in children.iter_mut().enumerate() {
                    println!(" [*] restart service {}...", i);
                    // TODO: speed this up with SIGKILL?
                    // should be safe with Docker because we block requests and never start a new container, but still risky, and we kill container cgroups to speed that up anyway
                    // not sure if it's safe for k8s though
                    kill(Pid::from_raw(child.process.id() as i32), Some(Signal::SIGTERM))?;
                    let status = child.process.wait()?;
                    println!(" [*] restart service {}: exited with {}", i, status);
    
                    child.process = Command::new(&child.args[0])
                        .args(&child.args[1..])
                        .spawn()
                        .unwrap();
                }
            }
            _ => {
                println!(" [*] received {}", Signal::try_from(signal)?);

                // forward signal to children
                for child in children.iter_mut() {
                    kill(Pid::from_raw(child.process.id() as i32), Some(Signal::try_from(signal)?))?;
                }

                if signal == signal_hook::consts::SIGTERM {
                    is_shutting_down = true;
                }
            }
        }
    }

    Ok(())
}
